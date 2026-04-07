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
      case "memo":
      case "image":
      case "chat_record":
      case "file":
      default:
        await this.appendToDaily(msg);
        break;
    }
  }

  private async appendToDaily(msg: SyncMessage): Promise<void> {
    const date = new Date(msg.created_at);
    const dateStr = this.formatDate(date);
    const timeStr = this.formatTime(date);
    const filePath = normalizePath(`${this.settings.dailyFolder}/${dateStr}.md`);

    await this.ensureFolder(this.settings.dailyFolder);

    let content = "";
    const line = `- ${timeStr} ${msg.content}\n`;

    const exists = await this.app.vault.adapter.exists(filePath);
    if (exists) {
      content = await this.app.vault.adapter.read(filePath);
      content += line;
      await this.app.vault.adapter.write(filePath, content);
    } else {
      content = `# ${dateStr}\n\n${line}`;
      await this.app.vault.adapter.write(filePath, content);
    }

    // Download images if present
    if (msg.images && msg.images.length > 0) {
      for (const filename of msg.images) {
        await this.downloadAndSaveImage(filename);
      }
    }
  }

  private async createArticle(msg: SyncMessage): Promise<void> {
    let filePath: string;

    if (msg.filename) {
      // Server returns full path like "articles/2026-04-07-title.md"
      filePath = normalizePath(msg.filename);
    } else {
      const baseName = msg.title || "untitled";
      filePath = normalizePath(`${this.settings.articlesFolder}/${baseName}.md`);
    }

    // Ensure parent folder exists
    const folder = filePath.substring(0, filePath.lastIndexOf("/"));
    if (folder) {
      await this.ensureFolder(folder);
    }

    // Handle filename collision by appending timestamp
    const exists = await this.app.vault.adapter.exists(filePath);
    if (exists) {
      const timestamp = Date.now();
      filePath = filePath.replace(".md", `-${timestamp}.md`);
    }

    let content = msg.content;

    // Add title as heading if present and not already in content
    if (msg.title && !content.startsWith("# ")) {
      content = `# ${msg.title}\n\n${content}`;
    }

    // Add source URL if present
    if (msg.source_url) {
      content += `\n\n---\nSource: ${msg.source_url}\n`;
    }

    await this.app.vault.adapter.write(filePath, content);

    // Download images if present
    if (msg.images && msg.images.length > 0) {
      for (const filename of msg.images) {
        await this.downloadAndSaveImage(filename);
      }
    }
  }

  private async downloadAndSaveImage(filename: string): Promise<void> {
    const filePath = normalizePath(
      `${this.settings.attachmentsFolder}/${filename}`
    );

    await this.ensureFolder(this.settings.attachmentsFolder);

    // Skip if file already exists
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
