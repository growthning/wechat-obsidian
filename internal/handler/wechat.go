package handler

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/user/wechat-obsidian/internal/config"
	"github.com/user/wechat-obsidian/internal/fetcher"
	"github.com/user/wechat-obsidian/internal/model"
	"github.com/user/wechat-obsidian/internal/store"
	"github.com/user/wechat-obsidian/internal/wechat"
)

// WeChatHandler handles incoming Enterprise WeChat callbacks.
type WeChatHandler struct {
	cfg     *config.WeChatConfig
	store   *store.Store
	fetcher *fetcher.Fetcher
}

// NewWeChatHandler creates a new WeChatHandler.
func NewWeChatHandler(cfg *config.WeChatConfig, s *store.Store, f *fetcher.Fetcher) *WeChatHandler {
	return &WeChatHandler{cfg: cfg, store: s, fetcher: f}
}

// VerifyURL handles GET requests for Enterprise WeChat URL verification.
func (h *WeChatHandler) VerifyURL(c *gin.Context) {
	msgSign := c.Query("msg_signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	echostr := c.Query("echostr")

	if !wechat.VerifyURLSignature(h.cfg.Token, timestamp, nonce, msgSign) {
		c.String(http.StatusForbidden, "invalid signature")
		return
	}

	// Decrypt the echostr
	plaintext, _, err := wechat.DecryptMessage(h.cfg.EncodingAESKey, echostr)
	if err != nil {
		c.String(http.StatusBadRequest, "failed to decrypt echostr: %v", err)
		return
	}

	c.String(http.StatusOK, string(plaintext))
}

// HandleCallback handles POST requests for incoming Enterprise WeChat messages.
func (h *WeChatHandler) HandleCallback(c *gin.Context) {
	msgSign := c.Query("msg_signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")

	body, err := c.GetRawData()
	if err != nil {
		c.String(http.StatusBadRequest, "failed to read body")
		return
	}

	cb, err := wechat.ParseCallback(body)
	if err != nil {
		c.String(http.StatusBadRequest, "failed to parse callback")
		return
	}

	if !wechat.VerifySignature(h.cfg.Token, timestamp, nonce, cb.Encrypt, msgSign) {
		c.String(http.StatusForbidden, "invalid signature")
		return
	}

	msgBytes, _, err := wechat.DecryptMessage(h.cfg.EncodingAESKey, cb.Encrypt)
	if err != nil {
		c.String(http.StatusBadRequest, "failed to decrypt message")
		return
	}

	msg, err := wechat.ParseMessage(msgBytes)
	if err != nil {
		c.String(http.StatusBadRequest, "failed to parse message")
		return
	}

	// Return success immediately; process async.
	c.String(http.StatusOK, "success")

	go h.processMessage(msg, string(body))
}

// processMessage handles the message processing in a goroutine.
func (h *WeChatHandler) processMessage(msg *wechat.IncomingMessage, rawXML string) {
	now := time.Now()

	switch msg.MsgType {
	case "text":
		m := &model.Message{
			Type:      "memo",
			Content:   msg.Content,
			RawXML:    rawXML,
			CreatedAt: now,
		}
		if _, err := h.store.InsertMessage(m); err != nil {
			log.Printf("ERROR: inserting text message: %v", err)
		}

	case "image":
		datePrefix := now.Format("20060102")
		filename := fmt.Sprintf("img-%s-%s.jpg", datePrefix, msg.MsgID)

		if err := h.fetcher.DownloadToFile(msg.PicURL, filename); err != nil {
			log.Printf("ERROR: downloading image %s: %v", msg.PicURL, err)
			// Fall back to memo with error note
			m := &model.Message{
				Type:      "memo",
				Content:   fmt.Sprintf("(image download failed: %v) %s", err, msg.PicURL),
				RawXML:    rawXML,
				CreatedAt: now,
			}
			if _, err2 := h.store.InsertMessage(m); err2 != nil {
				log.Printf("ERROR: inserting fallback memo: %v", err2)
			}
			return
		}

		m := &model.Message{
			Type:      "image",
			Content:   fmt.Sprintf("![[%s]]", filename),
			Filename:  filename,
			RawXML:    rawXML,
			CreatedAt: now,
		}
		msgID, err := h.store.InsertMessage(m)
		if err != nil {
			log.Printf("ERROR: inserting image message: %v", err)
			return
		}

		att := &model.Attachment{
			MessageID:   msgID,
			Filename:    filename,
			OriginalURL: msg.PicURL,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting image attachment: %v", err)
		}

	case "link":
		if strings.Contains(msg.LinkURL, "mp.weixin.qq.com") {
			go h.fetchArticle(msg.LinkURL, msg.LinkTitle, rawXML, now)
		} else {
			content := fmt.Sprintf("[%s](%s)", msg.LinkTitle, msg.LinkURL)
			m := &model.Message{
				Type:      "memo",
				Content:   content,
				Title:     msg.LinkTitle,
				SourceURL: msg.LinkURL,
				RawXML:    rawXML,
				CreatedAt: now,
			}
			if _, err := h.store.InsertMessage(m); err != nil {
				log.Printf("ERROR: inserting link memo: %v", err)
			}
		}

	default:
		log.Printf("INFO: unhandled message type: %s", msg.MsgType)
	}
}

// fetchArticle fetches a WeChat article and saves it as an article message.
func (h *WeChatHandler) fetchArticle(url, title, rawXML string, now time.Time) {
	result, err := h.fetcher.FetchArticle(url)
	if err != nil {
		log.Printf("ERROR: fetching article %s: %v", url, err)
		// Save as memo with error
		m := &model.Message{
			Type:      "memo",
			Content:   fmt.Sprintf("(article fetch failed: %v) [%s](%s)", err, title, url),
			Title:     title,
			SourceURL: url,
			RawXML:    rawXML,
			CreatedAt: now,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting article error memo: %v", err2)
		}
		return
	}

	m := &model.Message{
		Type:      "article",
		Content:   result.Content,
		Title:     result.Title,
		Filename:  result.Filename,
		SourceURL: url,
		RawXML:    rawXML,
		CreatedAt: now,
	}
	msgID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting article message: %v", err)
		return
	}

	for _, imgFilename := range result.Images {
		att := &model.Attachment{
			MessageID:   msgID,
			Filename:    imgFilename,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting article image attachment %s: %v", imgFilename, err)
		}
	}
}
