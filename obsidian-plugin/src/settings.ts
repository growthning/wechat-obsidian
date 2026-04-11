import { App, PluginSettingTab, Setting } from "obsidian";
import type WeChatSyncPlugin from "./main";

export interface WeChatSyncSettings {
  serverUrl: string;
  apiKey: string;
  pollInterval: number;
  dailyFolder: string;
  articlesFolder: string;
  attachmentsFolder: string;
  lastSyncedId: number;
  deviceId: string;
}

export const DEFAULT_SETTINGS: WeChatSyncSettings = {
  serverUrl: "",
  apiKey: "",
  pollInterval: 10,
  dailyFolder: "raw/daily",
  articlesFolder: "raw/articles",
  attachmentsFolder: "raw/attachments",
  lastSyncedId: 0,
  deviceId: "",
};

export class WeChatSyncSettingTab extends PluginSettingTab {
  plugin: WeChatSyncPlugin;

  constructor(app: App, plugin: WeChatSyncPlugin) {
    super(app, plugin);
    this.plugin = plugin;
  }

  display(): void {
    const { containerEl } = this;
    containerEl.empty();

    containerEl.createEl("h2", { text: "WeChat Sync Settings" });

    new Setting(containerEl)
      .setName("Server URL")
      .setDesc("The URL of the WeChat sync server (e.g. http://localhost:3000)")
      .addText((text) =>
        text
          .setPlaceholder("http://localhost:3000")
          .setValue(this.plugin.settings.serverUrl)
          .onChange(async (value) => {
            this.plugin.settings.serverUrl = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName("API Key")
      .setDesc("The API key for authenticating with the sync server")
      .addText((text) =>
        text
          .setPlaceholder("your-api-key")
          .setValue(this.plugin.settings.apiKey)
          .onChange(async (value) => {
            this.plugin.settings.apiKey = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName("Poll Interval")
      .setDesc("How often to check for new messages (in seconds)")
      .addDropdown((dropdown) =>
        dropdown
          .addOption("10", "10 seconds")
          .addOption("30", "30 seconds")
          .addOption("60", "1 minute")
          .addOption("180", "3 minutes")
          .addOption("300", "5 minutes")
          .setValue(String(this.plugin.settings.pollInterval))
          .onChange(async (value) => {
            this.plugin.settings.pollInterval = Number(value);
            await this.plugin.saveSettings();
            this.plugin.restartPolling();
          })
      );

    new Setting(containerEl)
      .setName("Daily Notes Folder")
      .setDesc("Folder for daily note files")
      .addText((text) =>
        text
          .setPlaceholder("daily")
          .setValue(this.plugin.settings.dailyFolder)
          .onChange(async (value) => {
            this.plugin.settings.dailyFolder = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName("Articles Folder")
      .setDesc("Folder for saved articles")
      .addText((text) =>
        text
          .setPlaceholder("articles")
          .setValue(this.plugin.settings.articlesFolder)
          .onChange(async (value) => {
            this.plugin.settings.articlesFolder = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName("Attachments Folder")
      .setDesc("Folder for downloaded images and files")
      .addText((text) =>
        text
          .setPlaceholder("attachments")
          .setValue(this.plugin.settings.attachmentsFolder)
          .onChange(async (value) => {
            this.plugin.settings.attachmentsFolder = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName("Reset Sync")
      .setDesc("Reset sync position to re-fetch all messages from server")
      .addButton((btn) =>
        btn.setButtonText("Reset").onClick(async () => {
          this.plugin.settings.lastSyncedId = 0;
          await this.plugin.saveSettings();
          this.plugin.restartPolling();
          btn.setButtonText("Done!");
          setTimeout(() => btn.setButtonText("Reset"), 2000);
        })
      );
  }
}
