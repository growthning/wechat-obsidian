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
	apiKey string
	store  *store.Store
}

// NewImagesHandler creates a new ImagesHandler.
func NewImagesHandler(apiKey string, s *store.Store) *ImagesHandler {
	return &ImagesHandler{apiKey: apiKey, store: s}
}

// ServeImage handles GET /api/images/:filename — serves a stored image file.
func (h *ImagesHandler) ServeImage(c *gin.Context) {
	if c.Query("apikey") != h.apiKey {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
		return
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
