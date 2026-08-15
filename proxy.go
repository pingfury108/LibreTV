package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type proxyConfig struct {
	timeout         time.Duration
	maxRetries      int
	userAgent       string
	blockedHosts    []string
	blockedPrefixes []string
	filteredHeaders map[string]bool
	client          *http.Client
}

func newProxyConfig() *proxyConfig {
	timeoutMs, _ := strconv.Atoi(getenv("REQUEST_TIMEOUT", "5000"))
	maxRetries, _ := strconv.Atoi(getenv("MAX_RETRIES", "2"))

	filtered := map[string]bool{}
	// 可配置的敏感头
	for _, h := range strings.Split(getenv("FILTERED_HEADERS",
		"content-security-policy,cookie,set-cookie,x-frame-options,access-control-allow-origin"), ",") {
		filtered[strings.TrimSpace(h)] = true
	}
	// 必须过滤（不可配置）：Transport 已解压响应，保留原始压缩头会导致内容被截断
	for _, h := range []string{"content-length", "content-encoding", "transfer-encoding", "connection"} {
		filtered[h] = true
	}

	// 使用 Go 默认行为：自动读取 HTTP_PROXY/HTTPS_PROXY/NO_PROXY 环境变量，
	// 外部环境如何配置就如何使用，不在此处做任何代理策略
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// 多 IP 拨号：DNS 污染/坏 IP 时自动尝试备用公共 DNS 的解析结果
	transport.DialContext = multiIPDialContext

	return &proxyConfig{
		timeout:         time.Duration(timeoutMs) * time.Millisecond,
		maxRetries:      maxRetries,
		userAgent:       getenv("USER_AGENT", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"),
		blockedHosts:    strings.Split(getenv("BLOCKED_HOSTS", "localhost,127.0.0.1,0.0.0.0,::1"), ","),
		blockedPrefixes: strings.Split(getenv("BLOCKED_IP_PREFIXES", "192.168.,10.,172."), ","),
		filteredHeaders: filtered,
		client:          &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond, Transport: transport},
	}
}

// validateProxyAuth 校验 auth=sha256(PASSWORD)，t 时间戳 10 分钟内有效（与 Node 版逻辑一致）
func (c *config) validateProxyAuth(r *http.Request) bool {
	if c.passwordHash == "" {
		c.logDebug("服务器未设置 PASSWORD 环境变量，代理访问被拒绝")
		return false
	}
	q := r.URL.Query()
	if q.Get("auth") != c.passwordHash {
		c.logDebug("代理请求鉴权失败：密码哈希不匹配")
		return false
	}
	if ts := q.Get("t"); ts != "" {
		if ms, err := strconv.ParseInt(ts, 10, 64); err == nil {
			if time.Since(time.UnixMilli(ms)) > 10*time.Minute {
				c.logDebug("代理请求鉴权失败：时间戳过期")
				return false
			}
		}
	}
	return true
}

func (p *proxyConfig) isValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	for _, h := range p.blockedHosts {
		if host == strings.TrimSpace(h) {
			return false
		}
	}
	for _, prefix := range p.blockedPrefixes {
		if strings.HasPrefix(host, strings.TrimSpace(prefix)) {
			return false
		}
	}
	return true
}

func (c *config) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !c.validateProxyAuth(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"success":false,"error":"代理访问未授权：请检查密码配置或鉴权参数"}`)
		return
	}

	// 从 RequestURI 手动提取目标 URL，避免路径被二次解码
	uri := strings.TrimPrefix(r.RequestURI, "/proxy/")
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	targetURL, err := url.PathUnescape(uri)
	if err != nil || !proxyCfg.isValidURL(targetURL) {
		http.Error(w, "无效的 URL", http.StatusBadRequest)
		return
	}

	c.logDebug("代理请求:", targetURL)

	var resp *http.Response
	for attempt := 0; attempt <= proxyCfg.maxRetries; attempt++ {
		var req *http.Request
		req, err = http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			break
		}
		req.Header.Set("User-Agent", proxyCfg.userAgent)
		// 部分图床（如豆瓣）校验 Referer，使用目标站自身 origin 绕过防盗链
		if u, e := url.Parse(targetURL); e == nil {
			req.Header.Set("Referer", u.Scheme+"://"+u.Host+"/")
		}
		// 豆瓣网关（Server: dae）拒绝请求行中的原始非 ASCII 字节，
		// 需把 query 重新百分号编码（中文 → %E7%83%AD 形式）
		req.URL.RawQuery = req.URL.Query().Encode()
		resp, err = proxyCfg.client.Do(req)
		if err == nil {
			break
		}
		c.logDebug("请求失败，重试", attempt+1, "/", proxyCfg.maxRetries, ":", err)
	}
	if err != nil {
		http.Error(w, "请求失败: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if proxyCfg.filteredHeaders[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

var proxyCfg = newProxyConfig()

// fallbackDNS 备用公共 DNS，主解析器返回坏 IP（DNS 污染）时提供备选解析结果
var fallbackDNS = []string{"8.8.8.8:53", "1.1.1.1:53"}

// dnsCache 解析结果缓存：避免突发并发请求重复查 DNS；
// goodIP 记录上次拨通的 IP，下次优先尝试、解析全坏时兜底
var dnsCache = &dnsCacheStore{entries: map[string]*dnsCacheEntry{}}

const dnsCacheTTL = 5 * time.Minute

type dnsCacheEntry struct {
	ips    []string
	goodIP string
	expiry time.Time
}

type dnsCacheStore struct {
	mu      sync.Mutex
	entries map[string]*dnsCacheEntry
}

func (s *dnsCacheStore) get(host string) (ips []string, goodIP string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, exists := s.entries[host]
	if !exists || time.Now().After(e.expiry) {
		return nil, "", false
	}
	return e.ips, e.goodIP, true
}

func (s *dnsCacheStore) setIPs(host string, ips []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, exists := s.entries[host]; exists {
		e.ips = ips
		e.expiry = time.Now().Add(dnsCacheTTL)
	} else {
		s.entries[host] = &dnsCacheEntry{ips: ips, expiry: time.Now().Add(dnsCacheTTL)}
	}
}

func (s *dnsCacheStore) setGoodIP(host, ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, exists := s.entries[host]; exists {
		e.goodIP = ip
	}
}

func (s *dnsCacheStore) evict(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, host)
}

// resolveHostIPs 并行向默认解析器 + 备用公共 DNS 查询主机 IP，去重合并
// 带缓存：5 分钟内同域名不重复解析，避免突发并发请求打爆 DNS
func resolveHostIPs(ctx context.Context, host string) []string {
	if ips, goodIP, ok := dnsCache.get(host); ok {
		// 好 IP 放最前面优先尝试
		if goodIP != "" {
			reordered := []string{goodIP}
			for _, ip := range ips {
				if ip != goodIP {
					reordered = append(reordered, ip)
				}
			}
			return reordered
		}
		return ips
	}
	var (
		mu   sync.Mutex
		seen = map[string]bool{}
		ips  []string
	)
	add := func(list []string) {
		mu.Lock()
		defer mu.Unlock()
		for _, ip := range list {
			if net.ParseIP(ip) != nil && !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
	}

	var wg sync.WaitGroup

	// 默认解析器（Go 纯解析或系统解析）
	wg.Add(1)
	go func() {
		defer wg.Done()
		if list, err := net.DefaultResolver.LookupHost(ctx, host); err == nil {
			add(list)
		}
	}()

	// 备用公共 DNS
	for _, dns := range fallbackDNS {
		wg.Add(1)
		go func(dns string) {
			defer wg.Done()
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{Timeout: 3 * time.Second}
					return d.DialContext(ctx, "udp", dns)
				},
			}
			if list, err := r.LookupHost(ctx, host); err == nil {
				add(list)
			}
		}(dns)
	}

	wg.Wait()
	if len(ips) > 0 {
		dnsCache.setIPs(host, ips)
	}
	return ips
}

// multiIPDialContext 拨号时对域名做多 IP 并行尝试：
// 解析得到的所有 IP 同时发起连接，第一个成功的胜出，避免 DNS 污染导致连到坏 IP
func multiIPDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// 本身就是 IP，直接拨
	if net.ParseIP(host) != nil {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	ips := resolveHostIPs(ctx, host)
	// 解析失败则回退默认拨号
	if len(ips) == 0 {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	dialCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type dialResult struct {
		conn net.Conn
		err  error
	}
	results := make(chan dialResult, len(ips))

	for _, ip := range ips {
		go func(ip string) {
			d := net.Dialer{Timeout: 5 * time.Second}
			c, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(ip, port))
			results <- dialResult{c, err}
		}(ip)
	}

	var lastErr error
	for range ips {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case res := <-results:
			if res.err == nil {
				// 记录拨通的好 IP，下次优先使用
				if tcpAddr, ok := res.conn.RemoteAddr().(*net.TCPAddr); ok {
					dnsCache.setGoodIP(host, tcpAddr.IP.String())
				}
				return res.conn, nil
			}
			lastErr = res.err
		}
	}
	// 全部拨号失败：可能是缓存了污染 IP，清除缓存让下次重新解析
	dnsCache.evict(host)
	return nil, lastErr
}
