package handler

import (
	"encoding/json"
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
	kf      *wechat.KFClient
}

// NewWeChatHandler creates a new WeChatHandler.
func NewWeChatHandler(cfg *config.WeChatConfig, s *store.Store, f *fetcher.Fetcher, kf *wechat.KFClient) *WeChatHandler {
	return &WeChatHandler{cfg: cfg, store: s, fetcher: f, kf: kf}
}

// VerifyURL handles GET requests for Enterprise WeChat URL verification.
func (h *WeChatHandler) VerifyURL(c *gin.Context) {
	msgSign := c.Query("msg_signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	echostr := c.Query("echostr")

	// Try self-built app keys first, then KF keys
	aesKey := h.cfg.EncodingAESKey
	if !wechat.VerifySignature(h.cfg.Token, timestamp, nonce, echostr, msgSign) {
		if h.cfg.KFToken != "" && wechat.VerifySignature(h.cfg.KFToken, timestamp, nonce, echostr, msgSign) {
			aesKey = h.cfg.KFEncodingAESKey
		} else {
			c.String(http.StatusForbidden, "invalid signature")
			return
		}
	}

	// Decrypt the echostr
	plaintext, _, err := wechat.DecryptMessage(aesKey, echostr)
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
		log.Printf("ERROR: failed to parse callback XML: %v, body: %s", err, string(body))
		c.String(http.StatusBadRequest, "failed to parse callback")
		return
	}

	// Try self-built app keys first, then KF keys
	aesKey := h.cfg.EncodingAESKey
	if !wechat.VerifySignature(h.cfg.Token, timestamp, nonce, cb.Encrypt, msgSign) {
		// Try KF keys
		if h.cfg.KFToken != "" && wechat.VerifySignature(h.cfg.KFToken, timestamp, nonce, cb.Encrypt, msgSign) {
			aesKey = h.cfg.KFEncodingAESKey
			log.Printf("INFO: callback matched KF keys")
		} else {
			log.Printf("ERROR: signature mismatch, msgSign=%s", msgSign)
			c.String(http.StatusForbidden, "invalid signature")
			return
		}
	} else {
		log.Printf("INFO: callback matched app keys")
	}

	msgBytes, corpID, err := wechat.DecryptMessage(aesKey, cb.Encrypt)
	if err != nil {
		log.Printf("ERROR: failed to decrypt: %v, aesKey=%s..., encrypt=%s...", err, aesKey[:8], cb.Encrypt[:20])
		c.String(http.StatusBadRequest, "failed to decrypt message: "+err.Error())
		return
	}
	log.Printf("INFO: decrypted message from corpID=%s, content=%s", corpID, string(msgBytes))

	msg, err := wechat.ParseMessage(msgBytes)
	if err != nil {
		log.Printf("ERROR: failed to parse message XML: %v, content=%s", err, string(msgBytes))
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
	case "event":
		if msg.Event == "kf_msg_or_event" {
			go h.processKFEvent(msg.Token, msg.OpenKFID, string(now.Format("20060102")))
		} else {
			log.Printf("INFO: unhandled event type: %s", msg.Event)
		}
		return

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

// processKFEvent handles a kf_msg_or_event callback by fetching actual messages via sync_msg API.
func (h *WeChatHandler) processKFEvent(callbackToken, openKFID, datePrefix string) {
	if h.kf == nil {
		log.Printf("ERROR: received KF event but KFClient is not configured")
		return
	}

	cursor := ""
	for {
		resp, err := h.kf.SyncMessages(callbackToken, cursor, openKFID)
		if err != nil {
			log.Printf("ERROR: syncing KF messages: %v", err)
			return
		}

		for _, msg := range resp.MsgList {
			// Only process messages from WeChat customers (origin=3)
			if msg.Origin != 3 {
				continue
			}
			h.processKFMessage(&msg, datePrefix)
		}

		if resp.HasMore != 1 || resp.NextCursor == "" {
			break
		}
		cursor = resp.NextCursor
	}
}

// processKFMessage processes a single message from the KF sync_msg API.
func (h *WeChatHandler) processKFMessage(msg *wechat.KFMessage, datePrefix string) {
	// Debug: log raw message JSON
	if rawJSON, err := json.Marshal(msg); err == nil {
		log.Printf("DEBUG: KF message: %s", string(rawJSON))
	}
	now := time.Now()

	switch msg.MsgType {
	case "text":
		if msg.Text == nil {
			return
		}
		m := &model.Message{
			Type:      "memo",
			Content:   msg.Text.Content,
			CreatedAt: now,
		}
		if _, err := h.store.InsertMessage(m); err != nil {
			log.Printf("ERROR: inserting KF text message: %v", err)
		}

	case "link":
		if msg.Link == nil {
			return
		}
		if strings.Contains(msg.Link.URL, "mp.weixin.qq.com") {
			go h.fetchArticle(msg.Link.URL, msg.Link.Title, "", now)
		} else {
			content := fmt.Sprintf("[%s](%s)", msg.Link.Title, msg.Link.URL)
			m := &model.Message{
				Type:      "memo",
				Content:   content,
				Title:     msg.Link.Title,
				SourceURL: msg.Link.URL,
				CreatedAt: now,
			}
			if _, err := h.store.InsertMessage(m); err != nil {
				log.Printf("ERROR: inserting KF link memo: %v", err)
			}
		}

	case "image":
		if msg.Image == nil {
			return
		}
		h.processKFImage(msg.Image.MediaID, datePrefix, now)

	case "channels":
		if msg.Channels == nil {
			return
		}
		content := fmt.Sprintf("**视频号**: %s\n**标题**: %s", msg.Channels.Nickname, msg.Channels.Title)
		m := &model.Message{
			Type:      "memo",
			Content:   content,
			Title:     msg.Channels.Title,
			CreatedAt: now,
		}
		if _, err := h.store.InsertMessage(m); err != nil {
			log.Printf("ERROR: inserting KF channels message: %v", err)
		}

	default:
		log.Printf("INFO: unhandled KF message type: %s (msgid=%s)", msg.MsgType, msg.MsgID)
	}
}

// processKFImage downloads a media file via KF API and saves it as an image message.
func (h *WeChatHandler) processKFImage(mediaID, datePrefix string, now time.Time) {
	data, contentType, err := h.kf.DownloadMedia(mediaID)
	if err != nil {
		log.Printf("ERROR: downloading KF media %s: %v", mediaID, err)
		m := &model.Message{
			Type:      "memo",
			Content:   fmt.Sprintf("(KF image download failed: %v) media_id=%s", err, mediaID),
			CreatedAt: now,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting KF image fallback memo: %v", err2)
		}
		return
	}

	ext := mediaExtFromContentType(contentType)
	filename := fmt.Sprintf("img-%s-%s%s", datePrefix, mediaID[:8], ext)

	if err := h.fetcher.SaveFile(filename, data); err != nil {
		log.Printf("ERROR: saving KF image %s: %v", filename, err)
		return
	}

	m := &model.Message{
		Type:      "image",
		Content:   fmt.Sprintf("![[%s]]", filename),
		Filename:  filename,
		CreatedAt: now,
	}
	msgID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting KF image message: %v", err)
		return
	}

	att := &model.Attachment{
		MessageID:   msgID,
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(data)),
		CreatedAt:   now,
	}
	if _, err := h.store.InsertAttachment(att); err != nil {
		log.Printf("ERROR: inserting KF image attachment: %v", err)
	}
}

// mediaExtFromContentType returns a file extension based on content-type.
func mediaExtFromContentType(ct string) string {
	switch {
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}
