package model

import "time"

type Message struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	Title     string    `json:"title,omitempty"`
	Filename  string    `json:"filename,omitempty"`
	SourceURL string    `json:"source_url,omitempty"`
	RawXML    string    `json:"-"`
	Synced    bool      `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

type Attachment struct {
	ID          int64     `json:"id"`
	MessageID   int64     `json:"message_id"`
	Filename    string    `json:"filename"`
	OriginalURL string    `json:"original_url,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
}

type SyncResponse struct {
	Messages []MessageWithImages `json:"messages"`
	HasMore  bool                `json:"has_more"`
}

type MessageWithImages struct {
	Message
	Images []string `json:"images,omitempty"`
}

type AckRequest struct {
	LastID int64 `json:"last_id"`
}
