package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

//go:embed index.html player.html watch.html about.html js css image libs manifest.json robots.txt service-worker.js VERSION.txt
var staticFS embed.FS

type config struct {
	port         string
	password     string
	passwordHash string // sha256(password)，页面注入与代理鉴权共用
	corsOrigin   string
	debug        bool
	cacheMaxAge  int // 静态资源缓存秒数
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseMaxAge 兼容 express 风格（1d/1h/1m/1s）或纯秒数
func parseMaxAge(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if len(s) < 2 {
		return 86400
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 86400
	}
	switch s[len(s)-1] {
	case 'd':
		return n * 86400
	case 'h':
		return n * 3600
	case 'm':
		return n * 60
	}
	return 86400
}

func (c *config) logDebug(args ...any) {
	if c.debug {
		log.Println(append([]any{"[DEBUG]"}, args...)...)
	}
}

// loadDotEnv 与 Node 版 dotenv 行为一致：加载 .env，不覆盖已有环境变量
func loadDotEnv() {
	_ = godotenv.Load() // 无 .env 文件时静默跳过
}

func main() {
	loadDotEnv()
	cfg := &config{
		port:        getenv("PORT", "8080"),
		password:    os.Getenv("PASSWORD"),
		corsOrigin:  getenv("CORS_ORIGIN", "*"),
		debug:       os.Getenv("DEBUG") == "true",
		cacheMaxAge: parseMaxAge(getenv("CACHE_MAX_AGE", "1d")),
	}
	if cfg.password != "" {
		sum := sha256.Sum256([]byte(cfg.password))
		cfg.passwordHash = hex.EncodeToString(sum[:])
		log.Println("用户登录密码已设置")
	} else {
		log.Println("警告: 未设置 PASSWORD 环境变量，代理访问将被拒绝")
	}

	handler := cfg.securityHeaders(cfg.cors(http.HandlerFunc(cfg.route)))

	log.Printf("服务器运行在 http://localhost:%s", cfg.port)
	log.Fatal(http.ListenAndServe(":"+cfg.port, handler))
}

// route 自实现路由，避免 http.ServeMux 对 /proxy/https%3A%2F%2F...
// 这类解码后含 "//" 的路径做 301 重定向清洗
func (c *config) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	switch {
	case strings.HasPrefix(p, "/proxy/"):
		c.handleProxy(w, r)
	case p == "/" || p == "/index.html" || strings.HasPrefix(p, "/s="):
		c.renderPage(w, "index.html")
	case p == "/player.html":
		c.renderPage(w, "player.html")
	default:
		c.serveStatic(w, r)
	}
}

// renderPage 输出 HTML 并将 {{PASSWORD}} 占位符替换为密码哈希（前端密码验证依赖）
func (c *config) renderPage(w http.ResponseWriter, name string) {
	data, err := staticFS.ReadFile(name)
	if err != nil {
		http.Error(w, "读取静态页面失败", http.StatusInternalServerError)
		return
	}
	content := strings.ReplaceAll(string(data), "{{PASSWORD}}", c.passwordHash)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, content)
}

func (c *config) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	f, err := staticFS.Open(name)
	if err != nil {
		http.Error(w, "页面未找到", http.StatusNotFound)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "页面未找到", http.StatusNotFound)
		return
	}

	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(c.cacheMaxAge))
	http.ServeContent(w, r, st.Name(), st.ModTime(), f.(io.ReadSeeker))
}

func (c *config) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", c.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *config) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}
