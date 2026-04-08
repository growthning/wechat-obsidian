package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/user/wechat-obsidian/internal/store"
)

// ImagesHandler handles image file serving requests.
type ImagesHandler struct {
	masterAPIKey string
	store        *store.Store
}

// NewImagesHandler creates a new ImagesHandler.
func NewImagesHandler(masterAPIKey string, s *store.Store) *ImagesHandler {
	return &ImagesHandler{masterAPIKey: masterAPIKey, store: s}
}

// ServeImage handles GET /api/images/:filename — serves a stored image file.
func (h *ImagesHandler) ServeImage(c *gin.Context) {
	apiKey := c.Query("apikey")
	if apiKey != h.masterAPIKey {
		user, err := h.store.GetUserByAPIKey(apiKey)
		if err != nil || user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}
	}

	// Sanitize filename to prevent path traversal
	rawFilename := c.Param("filename")
	filename := filepath.Base(rawFilename)

	imagePath := h.store.ImagePath(filename)

	if _, err := os.Stat(imagePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
		return
	}

	c.File(imagePath)
}

// ServeVideo handles GET /api/videos/:filename — serves a stored video file.
func (h *ImagesHandler) ServeVideo(c *gin.Context) {
	rawFilename := c.Param("filename")
	filename := filepath.Base(rawFilename)
	videoPath := filepath.Join(h.store.DataDir(), "videos", filename)

	if _, err := os.Stat(videoPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "video not found"})
		return
	}

	c.File(videoPath)
}
