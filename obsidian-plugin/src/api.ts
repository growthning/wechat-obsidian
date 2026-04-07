import { requestUrl } from "obsidian";

export interface SyncMessage {
  id: number;
  type: string;
  content: string;
  title?: string;
  filename?: string;
  source_url?: string;
  images?: string[];
  created_at: string;
}

export interface SyncResponse {
  messages: SyncMessage[];
  has_more: boolean;
}

export class ApiClient {
  constructor(private serverUrl: string, private apiKey: string) {}

  private baseUrl(): string {
    return this.serverUrl.replace(/\/+$/, "");
  }

  async fetchMessages(sinceId: number): Promise<SyncResponse> {
    const url = `${this.baseUrl()}/api/messages?since_id=${sinceId}&apikey=${encodeURIComponent(this.apiKey)}`;
    const response = await requestUrl({ url, method: "GET" });
    return response.json as SyncResponse;
  }

  async ackMessages(lastId: number): Promise<void> {
    const url = `${this.baseUrl()}/api/messages/ack?apikey=${encodeURIComponent(this.apiKey)}`;
    await requestUrl({
      url,
      method: "POST",
      contentType: "application/json",
      body: JSON.stringify({ last_id: lastId }),
    });
  }

  async downloadImage(filename: string): Promise<ArrayBuffer> {
    const url = `${this.baseUrl()}/api/images/${encodeURIComponent(filename)}?apikey=${encodeURIComponent(this.apiKey)}`;
    const response = await requestUrl({ url, method: "GET" });
    return response.arrayBuffer;
  }
}
