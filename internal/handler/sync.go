package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/user/wechat-obsidian/internal/model"
	"github.com/user/wechat-obsidian/internal/store"
)

// SyncHandler handles message sync API requests.
type SyncHandler struct {
	apiKey string
	store  *store.Store
}

// NewSyncHandler creates a new SyncHandler.
func NewSyncHandler(apiKey string, s *store.Store) *SyncHandler {
	return &SyncHandler{apiKey: apiKey, store: s}
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
