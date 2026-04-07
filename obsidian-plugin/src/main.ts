import { Plugin } from "obsidian";
import { ApiClient } from "./api";
import {
  DEFAULT_SETTINGS,
  WeChatSyncSettings,
  WeChatSyncSettingTab,
} from "./settings";
import { VaultWriter } from "./writer";

export default class WeChatSyncPlugin extends Plugin {
  settings: WeChatSyncSettings = DEFAULT_SETTINGS;
  private pollTimer: number | null = null;
  private syncing = false;

  async onload(): Promise<void> {
    await this.loadSettings();
    this.addSettingTab(new WeChatSyncSettingTab(this.app, this));
    this.startPolling();
    console.log("WeChat Sync plugin loaded");
  }

  onunload(): void {
    this.stopPolling();
    console.log("WeChat Sync plugin unloaded");
  }

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
  }

  startPolling(): void {
    this.stopPolling();

    if (!this.settings.serverUrl || !this.settings.apiKey) {
      return;
    }

    // Initial sync after 5 seconds
    const initialTimer = window.setTimeout(() => {
      this.sync();
    }, 5000);
    this.registerInterval(initialTimer as unknown as number);

    // Regular polling
    const interval = this.settings.pollInterval * 1000;
    this.pollTimer = window.setInterval(() => {
      this.sync();
    }, interval);
    this.registerInterval(this.pollTimer);
  }

  stopPolling(): void {
    if (this.pollTimer !== null) {
      window.clearInterval(this.pollTimer);
      this.pollTimer = null;
    }
  }

  restartPolling(): void {
    this.stopPolling();
    this.startPolling();
  }

  private async sync(): Promise<void> {
    if (this.syncing) {
      return;
    }

    if (!this.settings.serverUrl || !this.settings.apiKey) {
      return;
    }

    this.syncing = true;

    try {
      const api = new ApiClient(this.settings.serverUrl, this.settings.apiKey);
      const writer = new VaultWriter(this.app, this.settings, api);

      let hasMore = true;
      while (hasMore) {
        const response = await api.fetchMessages(this.settings.lastSyncedId);

        if (response.messages.length === 0) {
          break;
        }

        for (const msg of response.messages) {
          await writer.writeMessage(msg);
        }

        // Update lastSyncedId to the highest id received
        const lastMsg = response.messages[response.messages.length - 1];
        if (lastMsg) {
          this.settings.lastSyncedId = lastMsg.id;
          await this.saveSettings();
          await api.ackMessages(lastMsg.id);
        }

        hasMore = response.has_more;
      }
    } catch (e) {
      console.error("WeChat Sync: sync failed", e);
    } finally {
      this.syncing = false;
    }
  }
}
