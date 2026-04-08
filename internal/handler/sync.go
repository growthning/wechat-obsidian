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
	masterAPIKey string
	store        *store.Store
	fetcher      *fetcher.Fetcher
}

// NewSyncHandler creates a new SyncHandler.
func NewSyncHandler(masterAPIKey string, s *store.Store, f *fetcher.Fetcher) *SyncHandler {
	return &SyncHandler{masterAPIKey: masterAPIKey, store: s, fetcher: f}
}

// authenticate checks the API key and returns the userID and whether it's an admin.
func (h *SyncHandler) authenticate(apiKey string) (userID int64, isAdmin bool, err error) {
	if apiKey == h.masterAPIKey {
		return 0, true, nil
	}
	user, err := h.store.GetUserByAPIKey(apiKey)
	if err != nil {
		return 0, false, err
	}
	if user == nil {
		return 0, false, fmt.Errorf("invalid api key")
	}
	return user.ID, false, nil
}

// GetMessages handles GET /api/sync — returns unsynced messages since a given ID.
func (h *SyncHandler) GetMessages(c *gin.Context) {
	userID, _, err := h.authenticate(c.Query("apikey"))
	if err != nil {
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

	messages, hasMore, err := h.store.GetUnsynced(sinceID, 50, userID)
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
	userID, _, err := h.authenticate(c.Query("apikey"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
	}

	var req model.AckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if err := h.store.AckMessages(req.LastID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to ack messages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// SaveURL handles POST /api/save — manually save a URL (article or plain link).
func (h *SyncHandler) SaveURL(c *gin.Context) {
	userID, _, err := h.authenticate(c.Query("apikey"))
	if err != nil {
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
				UserID:    userID,
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
			UserID:    userID,
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

// ListUsers handles GET /api/admin/users — returns all registered users (admin only).
func (h *SyncHandler) ListUsers(c *gin.Context) {
	if c.Query("apikey") != h.masterAPIKey || h.masterAPIKey == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "admin access required"})
		return
	}

	users, err := h.store.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}
