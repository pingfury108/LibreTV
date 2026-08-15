# LibreTV - 免费在线视频搜索与观看平台

<div align="center">
  <img src="image/logo.png" alt="LibreTV Logo" width="120">
  <br>
  <p><strong>自由观影，畅享精彩</strong></p>
</div>

## 📺 项目简介

LibreTV 是一个轻量级、免费的在线视频搜索与观看平台，提供来自多个视频源的内容搜索与播放服务。无需注册，即开即用，支持多种设备访问。

本仓库 fork 自 [LibreSpark/LibreTV](https://github.com/LibreSpark/LibreTV)，后端已由 Node.js 重写为 **Go**（纯标准库 + godotenv），仅支持 Docker 部署与本地运行，不再支持 Serverless 平台。

## 🚨 重要声明

- 本项目仅供学习和个人使用，为避免版权纠纷，必须设置 PASSWORD 环境变量
- 请勿将部署的实例用于商业用途或公开服务
- 如因公开分享导致的任何法律问题，用户需自行承担责任
- 项目开发者不对用户的使用行为承担任何法律责任

## 📋 部署

### Docker

```
docker run -d \
  --name libretv \
  --restart unless-stopped \
  -p 8899:8080 \
  -e PASSWORD=your_password \
  ghcr.io/pingfury108/libretv:latest
```

### Docker Compose

```yaml
services:
  libretv:
    image: ghcr.io/pingfury108/libretv:latest
    container_name: libretv
    ports:
      - "8899:8080" # 将内部 8080 端口映射到主机的 8899 端口
    environment:
      - PASSWORD=${PASSWORD:-111111} # 可将 111111 修改为你想要的密码
    restart: unless-stopped
```

启动 LibreTV：

```bash
docker compose up -d
```

访问 `http://localhost:8899` 即可使用。

镜像支持 linux/amd64 与 linux/arm64（64 位树莓派可用），体积约 20MB，由 GitHub Actions 在 push 到 main 时自动构建。

### 本地开发环境

需要 Go 1.23+：

```bash
# 配置 .env 文件（可选，至少建议设置 PASSWORD）
# 参考字段：PORT / PASSWORD / DEBUG / CORS_ORIGIN / REQUEST_TIMEOUT / MAX_RETRIES

# 启动
go run .
```

访问 `http://localhost:8080` 即可使用（端口可在 .env 文件中通过 PORT 变量修改）。

> ⚠️ 注意：使用简单静态服务器（如 `python -m http.server`）时，视频代理功能将不可用，视频无法正常播放。完整功能测试请使用 `go run .`。

## 🔧 自定义配置

### 密码保护

**重要提示**: 为确保安全，所有部署都必须设置 PASSWORD 环境变量，否则用户将看到设置密码的提示。

### API兼容性

LibreTV 支持标准的苹果 CMS V10 API 格式。添加自定义 API 时需遵循以下格式：
- 搜索接口: `https://example.com/api.php/provide/vod/?ac=videolist&wd=关键词`
- 详情接口: `https://example.com/api.php/provide/vod/?ac=detail&ids=视频ID`

**添加 CMS 源**:
1. 在设置面板中选择"自定义接口"
2. 接口地址: `https://example.com/api.php/provide/vod`

内置数据源在 `js/customer_site.js` 中配置。

## ⌨️ 键盘快捷键

播放器支持以下键盘快捷键：

- **空格键**: 播放/暂停
- **左右箭头**: 快退/快进
- **上下箭头**: 音量增加/减小
- **M 键**: 静音/取消静音
- **F 键**: 全屏/退出全屏
- **Esc 键**: 退出全屏

## 🛠️ 技术栈

- HTML5 + CSS3 + JavaScript (ES6+)
- Tailwind CSS
- HLS.js 用于 HLS 流处理
- ArtPlayer 视频播放器核心
- Go 后端（静态资源内嵌 + HTTP 代理转发）
- localStorage 本地存储

## ⚠️ 免责声明

LibreTV 仅作为视频搜索工具，不存储、上传或分发任何视频内容。所有视频均来自第三方 API 接口提供的搜索结果。如有侵权内容，请联系相应的内容提供方。

本项目开发者不对使用本项目产生的任何后果负责。使用本项目时，您必须遵守当地的法律法规。
