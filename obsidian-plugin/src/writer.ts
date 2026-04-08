import { App, normalizePath } from "obsidian";
import { ApiClient, SyncMessage } from "./api";
import { WeChatSyncSettings } from "./settings";

export class VaultWriter {
  constructor(
    private app: App,
    private settings: WeChatSyncSettings,
    private api: ApiClient
  ) {}

  async writeMessage(msg: SyncMessage): Promise<void> {
    switch (msg.type) {
      case "article":
        await this.createArticle(msg);
        break;
      case "channels":
        await this.appendToDaily(msg, "视频号");
        break;
      case "video":
        await this.appendToDaily(msg, this.videoPlatform(msg));
        break;
      case "memo":
      case "image":
      case "chat_record":
      case "file":
      default:
        await this.appendToDaily(msg);
        break;
    }
  }

  private async appendToDaily(msg: SyncMessage, suffix?: string): Promise<void> {
    const date = new Date(msg.created_at);
    const dateStr = this.formatDate(date);
    const timeStr = this.formatTime(date);
    const fileName = suffix ? `${dateStr}-${suffix}` : dateStr;
    const dayFolder = normalizePath(`${this.settings.dailyFolder}/${dateStr}`);
    const filePath = normalizePath(`${dayFolder}/${fileName}.md`);

    await this.ensureFolder(dayFolder);

    let line: string;
    if (suffix) {
      // Channels: block format with separator
      line = `${msg.content}\n_${timeStr}_\n\n---\n\n`;
    } else {
      // Memo: list format
      line = `- ${timeStr} ${msg.content}\n`;
    }

    const exists = await this.app.vault.adapter.exists(filePath);
    if (exists) {
      let content = await this.app.vault.adapter.read(filePath);
      content += line;
      await this.app.vault.adapter.write(filePath, content);
    } else {
      const header = `# ${fileName}\n\n`;
      await this.app.vault.adapter.write(filePath, header + line);
    }

    // Download images to the day's folder
    if (msg.images && msg.images.length > 0) {
      for (const filename of msg.images) {
        await this.downloadAndSaveImage(filename, dayFolder);
      }
    }
  }

  private async createArticle(msg: SyncMessage): Promise<void> {
    // Derive a subfolder name from the filename (e.g. "articles/2026-04-07-1532-标题")
    let baseName: string;
    if (msg.filename) {
      // "articles/2026-04-07-1532-标题.md" → "2026-04-07-1532-标题"
      baseName = msg.filename.replace(/^articles\//, "").replace(/\.md$/, "");
    } else {
      baseName = msg.title || "untitled";
    }

    const articleFolder = normalizePath(`${this.settings.articlesFolder}/${baseName}`);
    await this.ensureFolder(articleFolder);

    const filePath = normalizePath(`${articleFolder}/${baseName}.md`);

    // Skip if article already exists
    const exists = await this.app.vault.adapter.exists(filePath);
    if (exists) {
      return;
    }

    let content = msg.content;

    if (msg.title && !content.startsWith("# ")) {
      content = `# ${msg.title}\n\n${content}`;
    }

    if (msg.source_url) {
      content += `\n\n---\nSource: ${msg.source_url}\n`;
    }

    await this.app.vault.adapter.write(filePath, content);

    // Download article images into the same subfolder
    if (msg.images && msg.images.length > 0) {
      for (const filename of msg.images) {
        await this.downloadAndSaveImage(filename, articleFolder);
      }
    }
  }

  private async downloadAndSaveImage(filename: string, targetFolder: string): Promise<void> {
    const filePath = normalizePath(`${targetFolder}/${filename}`);

    await this.ensureFolder(targetFolder);

    const exists = await this.app.vault.adapter.exists(filePath);
    if (exists) {
      return;
    }

    try {
      const data = await this.api.downloadImage(filename);
      await this.app.vault.adapter.writeBinary(filePath, data);
    } catch (e) {
      console.error(`WeChat Sync: failed to download image ${filename}`, e);
    }
  }

  private async ensureFolder(path: string): Promise<void> {
    const normalizedPath = normalizePath(path);
    const exists = await this.app.vault.adapter.exists(normalizedPath);
    if (!exists) {
      await this.app.vault.createFolder(normalizedPath);
    }
  }

  private videoPlatform(msg: SyncMessage): string {
    const url = (msg.source_url || msg.content || "").toLowerCase();
    if (url.includes("bilibili.com") || url.includes("b23.tv")) return "B站";
    if (url.includes("toutiao.com") || url.includes("ixigua.com")) return "头条";
    if (url.includes("youtube.com") || url.includes("youtu.be")) return "YouTube";
    if (url.includes("douyin.com")) return "抖音";
    if (url.includes("tiktok.com")) return "TikTok";
    return "视频";
  }

  private formatDate(d: Date): string {
    const year = d.getFullYear();
    const month = String(d.getMonth() + 1).padStart(2, "0");
    const day = String(d.getDate()).padStart(2, "0");
    return `${year}-${month}-${day}`;
  }

  private formatTime(d: Date): string {
    const hours = String(d.getHours()).padStart(2, "0");
    const minutes = String(d.getMinutes()).padStart(2, "0");
    return `${hours}:${minutes}`;
  }
}
