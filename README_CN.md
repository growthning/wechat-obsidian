# WeChat-Obsidian Sync

自部署工具，将微信内容自动同步到 Obsidian 笔记。通过企业微信客服账号转发文章、视频、图片和消息，即可自动保存到你的 Obsidian 仓库。

## 功能特性

- **公众号文章抓取** — 全文 Markdown 转换，自动下载图片
- **通用网页抓取** — 三级兜底：trafilatura → Jina Reader → yt-dlp 元数据提取，覆盖绝大多数网站
- **视频下载** — 支持 B站、YouTube、抖音、X/Twitter、西瓜/头条、TikTok 等平台（基于 yt-dlp）
- **多格式同步** — 文字、图片、链接、视频号、文件
- **智能 URL 处理** — 短链解析（b23.tv、t.co、bit.ly）、跟踪参数清理
- **实时反馈** — 即时"已收到"确认 + 处理完成通知
- **多用户支持** — 每个用户独立 API Key，数据隔离
- **Obsidian 日记** — 消息按日期归类，文章保存为独立 Markdown 文件
- **单文件部署** — Go 服务端 + SQLite，无需外部数据库

## 架构

```
[微信] → 转发内容 → [企业微信客服]
                          ↓ 回调
                   [Go 服务端] → 处理 → SQLite
                                          ↓
[Obsidian 插件] ← 每 10 秒轮询 /api/sync ← 消息 + 文章
       ↓
  写入 Obsidian 仓库（日记 + 文章文件）
```

## 快速开始

### 1. 部署服务端

```bash
# 编译
go build -o server ./cmd/server

# 配置
cp config.example.yaml config.yaml
vim config.yaml

# 运行
./server --config config.yaml
```

### 2. 配置企业微信

1. 注册 [企业微信](https://work.weixin.qq.com)
2. 创建微信客服账号
3. 设置回调地址为 `https://你的服务器:8900/api/wechat/callback`
4. 将 CorpID、Secret、Token、EncodingAESKey 填入 `config.yaml`

### 3. 安装 Obsidian 插件

**方式一：BRAT 插件（推荐，支持自动更新）**

1. 安装 [Obsidian42 - BRAT](https://github.com/TfTHacker/obsidian42-brat)
2. BRAT 设置 → Add Beta Plugin → 输入 `growthning/wechat-obsidian`
3. 启用 "WeChat Sync" 插件
4. 在插件设置中填入服务器地址和 API Key

**方式二：手动安装**

1. 从 [Releases](https://github.com/growthning/wechat-obsidian/releases) 下载 `main.js` 和 `manifest.json`
2. 在 Obsidian 仓库中创建 `.obsidian/plugins/wechat-sync/` 目录
3. 将两个文件放入该目录
4. 重启 Obsidian，启用插件，配置服务器地址和 API Key

### 4. 获取 API Key

向企业微信客服账号发送"注册"，即可收到你的 API Key。

## 配置说明

```yaml
server:
  port: 8900                          # 服务端口
  api_key: "你的管理密钥"              # 管理接口密钥
  base_url: "http://你的服务器:8900"   # 服务器公网地址

wechat:
  corp_id: "ww..."                    # 企业 ID
  agent_id: 1000002                   # 应用 ID
  secret: "应用密钥"                   # 应用 Secret
  token: "回调Token"                  # 回调配置 Token
  encoding_aes_key: "回调加密密钥"     # 回调配置 EncodingAESKey

storage:
  data_dir: "./data"                  # 数据存储目录
  cleanup_days: 30                    # 已同步数据保留天数

article:
  timeout: 30s                        # 文章抓取超时
  max_images: 50                      # 每篇文章最多下载图片数
```

## API 接口

| 接口 | 方法 | 认证 | 说明 |
|------|------|------|------|
| `/api/wechat/callback` | GET/POST | 企微签名 | 企业微信回调 |
| `/api/sync` | GET | API Key | 拉取未同步消息 |
| `/api/sync/ack` | POST | API Key | 确认已同步消息 |
| `/api/images/:filename` | GET | API Key | 下载图片 |
| `/api/videos/:filename` | GET | API Key | 下载视频 |
| `/api/save` | POST | API Key | 手动保存 URL |
| `/health` | GET | 无 | 健康检查 |

## 内容处理流程

| 内容类型 | 处理方式 | 输出 |
|---------|---------|------|
| 公众号文章链接 | 全文提取 + 图片下载 | 独立 .md 文件 |
| 普通网页链接 | trafilatura → Jina Reader → yt-dlp 元数据 | 独立 .md 文件或书签 |
| B站/YouTube/抖音/X 视频 | yt-dlp 下载 | 视频文件 + 日记条目 |
| 文字消息 | 直接保存 | 日记条目 |
| 图片 | 通过企微 API 下载 | 图片文件 + 日记条目 |
| 视频号 | 提取元数据 | 日记条目 |

## 服务器要求

- Go 1.21+
- [yt-dlp](https://github.com/yt-dlp/yt-dlp)（用于视频下载和元数据兜底提取）
- 公网 IP，8900 端口可访问（用于企微回调）

## 部署

```bash
# 交叉编译 Linux 服务端
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o server ./cmd/server

# 部署（参考 deploy.example.sh）
scp server 你的服务器:/path/to/wechat-obsidian/
ssh 你的服务器 "systemctl restart wechat-obsidian"
```

## 开源协议

MIT
