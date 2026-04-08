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

    console.log("WeChat Sync: startPolling, serverUrl=" + this.settings.serverUrl + ", apiKey=" + (this.settings.apiKey ? "***" : "(empty)"));

    if (!this.settings.serverUrl || !this.settings.apiKey) {
      console.log("WeChat Sync: no serverUrl or apiKey, polling disabled");
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
    console.log("WeChat Sync: syncing, lastSyncedId=" + this.settings.lastSyncedId);

    try {
      const api = new ApiClient(this.settings.serverUrl, this.settings.apiKey);
      const writer = new VaultWriter(this.app, this.settings, api);

      let hasMore = true;
      while (hasMore) {
        const response = await api.fetchMessages(this.settings.lastSyncedId);
        console.log("WeChat Sync: fetched " + response.messages.length + " messages");

        if (response.messages.length === 0) {
          break;
        }

        // Sort by send time before writing (id order may differ from send time due to async fetching)
        const sorted = [...response.messages].sort(
          (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
        );
        for (const msg of sorted) {
          try {
            await writer.writeMessage(msg);
          } catch (e) {
            console.error(`WeChat Sync: failed to write message ${msg.id}`, e);
          }
        }

        // ACK with max id in batch (not last sorted, which may differ from highest id)
        const maxId = Math.max(...response.messages.map((m) => m.id));
        if (maxId > 0) {
          this.settings.lastSyncedId = maxId;
          await this.saveSettings();
          await api.ackMessages(maxId);
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
