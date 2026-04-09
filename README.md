# WeChat-Obsidian Sync

Self-hosted tool to sync content from Enterprise WeChat (企业微信) to your Obsidian vault. Forward articles, videos, images, and messages to a WeChat customer service account, and they'll automatically appear in your Obsidian notes.

## Features

- **Article extraction** — WeChat public account articles with full-text Markdown and images
- **Generic web scraping** — Three-tier fallback: trafilatura → Jina Reader → yt-dlp metadata, covering most websites
- **Video download** — B站, YouTube, 抖音, X/Twitter, 西瓜/头条, TikTok, and more (powered by yt-dlp)
- **Multi-format sync** — Text, images, links, 视频号 (Channels), files
- **Smart URL handling** — Short link resolution (b23.tv, t.co, bit.ly), tracking param cleanup
- **Real-time feedback** — Instant acknowledgment + completion notification via WeChat
- **Multi-user support** — Each user gets a unique API key, data is isolated
- **Obsidian daily notes** — Messages organized by date, articles saved as standalone Markdown files
- **Single binary, zero deps** — Go server + SQLite, no external database needed

## Architecture

```
[WeChat] → forward content → [Enterprise WeChat KF]
                                      ↓ callback
                              [Go Server] → process → SQLite
                                                         ↓
[Obsidian Plugin] ← poll /api/sync every 10s ← messages + articles
        ↓
  Write to vault (daily notes + article files)
```

## Quick Start

### 1. Server Setup

```bash
# Build
go build -o server ./cmd/server

# Configure
cp config.yaml my-config.yaml
vim my-config.yaml

# Run
./server --config my-config.yaml
```

### 2. Enterprise WeChat Setup

1. Register at [work.weixin.qq.com](https://work.weixin.qq.com)
2. Create a Customer Service account (微信客服)
3. Set callback URL to `https://your-server:8900/api/wechat/callback`
4. Copy CorpID, Secret, Token, EncodingAESKey to `config.yaml`
5. Add the KF account configuration (kf_secret, kf_token, kf_encoding_aes_key)

### 3. Install Obsidian Plugin

**Option A: BRAT (recommended, auto-updates)**

1. Install [Obsidian42 - BRAT](https://github.com/TfTHacker/obsidian42-brat)
2. BRAT Settings → Add Beta Plugin → `growthning/wechat-obsidian`
3. Enable "WeChat Sync" in Community Plugins
4. Configure server URL and API key in plugin settings

**Option B: Manual**

1. Download `main.js` and `manifest.json` from [Releases](https://github.com/growthning/wechat-obsidian/releases)
2. Create `.obsidian/plugins/wechat-sync/` in your vault
3. Copy both files into that directory
4. Restart Obsidian, enable the plugin, configure server URL and API key

### 4. Get Your API Key

Send "注册" to the WeChat KF account. You'll receive your API key in the reply.

## Configuration

```yaml
server:
  port: 8900
  api_key: "your-master-api-key"
  base_url: "http://your-server:8900"

wechat:
  corp_id: "ww..."
  agent_id: 1000002
  secret: "your-secret"
  token: "your-token"
  encoding_aes_key: "your-encoding-aes-key"
  kf_secret: "your-kf-secret"
  kf_token: "your-kf-token"
  kf_encoding_aes_key: "your-kf-encoding-aes-key"

storage:
  data_dir: "./data"
  cleanup_days: 30

article:
  timeout: 30s
  max_images: 50
```

## API Endpoints

| Endpoint | Method | Auth | Description |
|----------|--------|------|-------------|
| `/api/wechat/callback` | GET/POST | WeChat signature | Enterprise WeChat callback |
| `/api/sync` | GET | API key | Fetch unsynced messages |
| `/api/sync/ack` | POST | API key | Acknowledge synced messages |
| `/api/images/:filename` | GET | API key | Download image |
| `/api/videos/:filename` | GET | API key | Download video |
| `/api/save` | POST | API key | Save a URL manually |
| `/health` | GET | None | Health check |

## Content Processing

| Content Type | Processing | Output |
|-------------|-----------|--------|
| WeChat article link | Full-text extraction with images | Standalone .md file |
| Generic web link | trafilatura → Jina Reader → yt-dlp metadata | Standalone .md file or bookmark |
| B站/YouTube/抖音/X video | yt-dlp download | Video file + daily note entry |
| Text message | Direct save | Daily note entry |
| Image | Download via WeChat API | Image file + daily note entry |
| 视频号 (Channels) | Metadata extraction | Daily note entry |

## Server Requirements

- Go 1.21+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp) installed on the server (for video download and metadata fallback)
- Public IP with port 8900 accessible (for WeChat callback)

## Deploy

```bash
# Build for Linux server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

# Deploy (see deploy.sh for a full example)
scp server your-server:/path/to/wechat-obsidian/
ssh your-server "systemctl restart wechat-obsidian"
```

## License

MIT
