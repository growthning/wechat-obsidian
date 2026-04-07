# WeChat-Obsidian Sync

Self-hosted tool to sync content from Enterprise WeChat to your Obsidian vault.

## Features

- Forward WeChat messages (text, images, files, chat records) to Obsidian
- Auto-fetch WeChat article full-text as Markdown with images
- Obsidian plugin polls for new content every 10 seconds
- Single binary deployment, SQLite storage, zero external dependencies

## Quick Start

### 1. Server Setup

```bash
# Build
cd wechat-obsidian
go build -o server ./cmd/server

# Edit config
cp config.yaml my-config.yaml
vim my-config.yaml

# Run
./server --config my-config.yaml
```

Or with Docker:

```bash
docker build -t wechat-obsidian .
docker run -d -p 8900:8900 -v ./data:/app/data wechat-obsidian
```

### 2. Enterprise WeChat Setup

1. Register at [work.weixin.qq.com](https://work.weixin.qq.com)
2. Create a self-built application (自建应用)
3. Set callback URL to `https://your-vps.com:8900/api/wechat/callback`
4. Copy CorpID, Secret, Token, EncodingAESKey to `config.yaml`

### 3. Obsidian Plugin

1. Copy `obsidian-plugin/main.js` and `obsidian-plugin/manifest.json` to your vault's `.obsidian/plugins/wechat-sync/`
2. Enable the plugin in Obsidian Settings → Community Plugins
3. Configure server URL and API key in plugin settings

## Configuration

```yaml
server:
  port: 8900
  api_key: "change-me-to-a-random-string"

wechat:
  corp_id: "ww..."
  agent_id: 1000002
  secret: "your-secret"
  token: "your-token"
  encoding_aes_key: "your-encoding-aes-key"

storage:
  data_dir: "./data"

article:
  timeout: 30s
  max_images: 50
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/wechat/callback` | GET/POST | Enterprise WeChat callback |
| `/api/sync` | GET | Fetch unsynced messages |
| `/api/sync/ack` | POST | Acknowledge synced messages |
| `/api/images/:filename` | GET | Download image |
| `/health` | GET | Health check |

## How It Works

```
[Enterprise WeChat] → POST callback → [Go Server] → SQLite
                                                        ↓
[Obsidian Plugin] ← GET /api/sync (poll every 10s) ← messages
        ↓
  Write .md files to vault
```

1. You forward messages/articles to Enterprise WeChat
2. WeChat pushes the message to your server's callback URL
3. Server processes the message (fetches article full-text if needed)
4. Obsidian plugin polls for new messages and writes them to your vault

## License

MIT
