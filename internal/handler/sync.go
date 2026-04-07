package handler

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/user/wechat-obsidian/internal/fetcher"
	"github.com/user/wechat-obsidian/internal/model"
	"github.com/user/wechat-obsidian/internal/store"
)

// SyncHandler handles message sync API requests.
type SyncHandler struct {
	apiKey  string
	store   *store.Store
	fetcher *fetcher.Fetcher
}

// NewSyncHandler creates a new SyncHandler.
func NewSyncHandler(apiKey string, s *store.Store, f *fetcher.Fetcher) *SyncHandler {
	return &SyncHandler{apiKey: apiKey, store: s, fetcher: f}
}

// GetMessages handles GET /api/sync — returns unsynced messages since a given ID.
func (h *SyncHandler) GetMessages(c *gin.Context) {
	if c.Query("apikey") != h.apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}

	var sinceID int64
	if s := c.Query("since"); s != "" {
		parsed, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid since parameter"})
			return
		}
		sinceID = parsed
	}

	messages, hasMore, err := h.store.GetUnsynced(sinceID, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch messages"})
		return
	}

	if messages == nil {
		messages = []model.MessageWithImages{}
	}

	c.JSON(http.StatusOK, model.SyncResponse{
		Messages: messages,
		HasMore:  hasMore,
	})
}

// AckMessages handles POST /api/sync/ack — marks messages up to last_id as synced.
func (h *SyncHandler) AckMessages(c *gin.Context) {
	if c.Query("apikey") != h.apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}

	var req model.AckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.store.AckMessages(req.LastID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ack messages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SaveURL handles POST /api/save — manually save a URL (article or plain link).
func (h *SyncHandler) SaveURL(c *gin.Context) {
	if c.Query("apikey") != h.apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url is required"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "msg": "fetching"})

	go func() {
		now := time.Now()
		result, err := h.fetcher.FetchArticle(req.URL)
		if err != nil {
			log.Printf("ERROR: SaveURL fetch failed: %v", err)
			m := &model.Message{
				Type:      "memo",
				Content:   fmt.Sprintf("[%s](%s)", req.URL, req.URL),
				SourceURL: req.URL,
				CreatedAt: now,
			}
			h.store.InsertMessage(m)
			return
		}

		m := &model.Message{
			Type:      "article",
			Content:   result.Content,
			Title:     result.Title,
			Filename:  result.Filename,
			SourceURL: req.URL,
			CreatedAt: now,
		}
		msgID, err := h.store.InsertMessage(m)
		if err != nil {
			log.Printf("ERROR: SaveURL insert failed: %v", err)
			return
		}
		for _, img := range result.Images {
			att := &model.Attachment{
				MessageID:   msgID,
				Filename:    img,
				ContentType: "image/jpeg",
				CreatedAt:   now,
			}
			h.store.InsertAttachment(att)
		}
		log.Printf("INFO: SaveURL saved article: %s (%d images)", result.Title, len(result.Images))
	}()
}
