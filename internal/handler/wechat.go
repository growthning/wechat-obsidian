package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
		cleanedURL := cleanURL(msg.LinkURL)
		if strings.Contains(msg.LinkURL, "mp.weixin.qq.com") {
			go h.fetchArticle(msg.LinkURL, msg.LinkTitle, rawXML, now)
		} else {
			go h.fetchGenericOrMemo(cleanedURL, msg.LinkTitle, "", now)
		}

	default:
		log.Printf("INFO: unhandled message type: %s", msg.MsgType)
	}
}

// fetchArticle fetches a WeChat article and saves it as an article message.
func (h *WeChatHandler) fetchArticle(url, title, msgID string, now time.Time) {
	result, err := h.fetcher.FetchArticle(url, now)
	if err != nil {
		log.Printf("ERROR: fetching article %s: %v", url, err)
		// Save as memo with error
		m := &model.Message{
			MsgID:     msgID,
			Type:      "memo",
			Content:   fmt.Sprintf("(article fetch failed: %v) [%s](%s)", err, title, url),
			Title:     title,
			SourceURL: url,
			CreatedAt: now,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting article error memo: %v", err2)
		}
		return
	}

	m := &model.Message{
		MsgID:     msgID,
		Type:      "article",
		Content:   result.Content,
		Title:     result.Title,
		Filename:  result.Filename,
		SourceURL: url,
		CreatedAt: now,
	}
	dbID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting article message: %v", err)
		return
	}

	for _, imgFilename := range result.Images {
		att := &model.Attachment{
			MessageID:   dbID,
			Filename:    imgFilename,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting article image attachment %s: %v", imgFilename, err)
		}
	}
}

// fetchGenericOrMemo tries to fetch a non-WeChat URL as an article; falls back to memo.
func (h *WeChatHandler) fetchGenericOrMemo(url, title, msgID string, now time.Time) {
	result, err := h.fetcher.FetchGenericArticle(url, now)
	if err != nil {
		log.Printf("INFO: generic fetch failed for %s: %v, saving as memo", url, err)
		content := fmt.Sprintf("[%s](%s)", title, url)
		m := &model.Message{
			MsgID:     msgID,
			Type:      "memo",
			Content:   content,
			Title:     title,
			SourceURL: url,
			CreatedAt: now,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting link memo: %v", err2)
		}
		return
	}

	m := &model.Message{
		MsgID:     msgID,
		Type:      "article",
		Content:   result.Content,
		Title:     result.Title,
		Filename:  result.Filename,
		SourceURL: url,
		CreatedAt: now,
	}
	dbID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting generic article: %v", err)
		return
	}
	for _, img := range result.Images {
		att := &model.Attachment{
			MessageID:   dbID,
			Filename:    img,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting generic article image %s: %v", img, err)
		}
	}
	log.Printf("INFO: saved generic article: %s (%d images)", result.Title, len(result.Images))
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

		// Process events first (for welcome_code), then customer messages
		for _, msg := range resp.MsgList {
			if msg.MsgType == "event" && msg.Event != nil {
				h.processKFEventMsg(&msg)
			}
		}
		for _, msg := range resp.MsgList {
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

// processKFEventMsg handles KF event messages (enter_session, etc.).
func (h *WeChatHandler) processKFEventMsg(msg *wechat.KFMessage) {
	if msg.Event == nil {
		return
	}
	log.Printf("INFO: KF event: type=%s, user=%s, welcome_code=%s",
		msg.Event.EventType, msg.Event.ExternalUID, msg.Event.WelcomeCode)

	if msg.Event.EventType == "enter_session" && msg.Event.WelcomeCode != "" {
		// Auto-register user if new
		extUID := msg.Event.ExternalUID
		user, err := h.store.GetUserByExternalID(extUID)
		if err != nil {
			log.Printf("ERROR: looking up user for welcome: %v", err)
			return
		}
		if user == nil {
			user, err = h.store.CreateUser(extUID)
			if err != nil {
				log.Printf("ERROR: creating user for welcome: %v", err)
				return
			}
			log.Printf("INFO: auto-registered user %s via enter_session", extUID)
		}

		reply := fmt.Sprintf("欢迎使用 WeChat-Obsidian 同步服务！\n\n你的 API Key：\n%s\n\n请在 Obsidian 插件设置中填入此 Key。", user.APIKey)
		if err := h.kf.SendMsgOnEvent(msg.Event.WelcomeCode, reply); err != nil {
			log.Printf("WARN: failed to send welcome via event: %v", err)
		} else {
			log.Printf("INFO: sent welcome to user %s", extUID)
		}
	}
}

// processKFMessage processes a single message from the KF sync_msg API.
func (h *WeChatHandler) processKFMessage(msg *wechat.KFMessage, datePrefix string) {
	// Debug: log raw message JSON
	if rawJSON, err := json.Marshal(msg); err == nil {
		log.Printf("DEBUG: KF message: %s", string(rawJSON))
	}
	// Use message send_time instead of current time
	now := time.Unix(msg.SendTime, 0)

	// Auto-register new users on first message (silent, no reply)
	user, err := h.store.GetUserByExternalID(msg.ExternalUserID)
	if err != nil {
		log.Printf("ERROR: looking up user %s: %v", msg.ExternalUserID, err)
		return
	}
	if user == nil {
		user, err = h.store.CreateUser(msg.ExternalUserID)
		if err != nil {
			log.Printf("ERROR: auto-creating user %s: %v", msg.ExternalUserID, err)
			return
		}
		log.Printf("INFO: auto-registered user %s, api_key=%s", msg.ExternalUserID, user.APIKey)
	}

	// Reply API key when user sends "注册" (only for recent messages to avoid batch duplicates)
	if msg.MsgType == "text" && msg.Text != nil && strings.TrimSpace(msg.Text.Content) == "注册" {
		if time.Since(now).Abs() < 30*time.Second {
			reply := fmt.Sprintf("你的 API Key：\n%s\n\n请在 Obsidian 插件设置中填入此 Key。", user.APIKey)
			if err := h.kf.SendTextMessage(msg.OpenKFID, msg.ExternalUserID, reply); err != nil {
				log.Printf("WARN: failed to reply API key: %v", err)
			}
		}
		return
	}

	switch msg.MsgType {
	case "text":
		if msg.Text == nil {
			return
		}
		m := &model.Message{
			MsgID:     msg.MsgID,
			Type:      "memo",
			Content:   msg.Text.Content,
			CreatedAt: now,
			UserID:    user.ID,
		}
		if _, err := h.store.InsertMessage(m); err != nil {
			log.Printf("ERROR: inserting KF text message: %v", err)
		}

	case "link":
		if msg.Link == nil {
			return
		}
		cleanedURL := cleanURL(msg.Link.URL)
		if strings.Contains(msg.Link.URL, "mp.weixin.qq.com") {
			go h.fetchArticleForUser(msg.Link.URL, msg.Link.Title, msg.MsgID, now, user.ID)
		} else {
			go h.fetchGenericOrMemoForUser(cleanedURL, msg.Link.Title, msg.MsgID, now, user.ID)
		}

	case "image":
		if msg.Image == nil {
			return
		}
		h.processKFImageForUser(msg.Image.MediaID, datePrefix, now, user.ID)

	case "channels":
		if msg.Channels == nil {
			return
		}
		subTypeStr := "视频号"
		if msg.Channels.SubType == 2 {
			subTypeStr = "视频号直播"
		}
		title := truncateStr(msg.Channels.Title, 80)
		content := fmt.Sprintf("**%s** · %s\n%s", msg.Channels.Nickname, subTypeStr, title)
		m := &model.Message{
			MsgID:     msg.MsgID,
			Type:      "channels",
			Content:   content,
			Title:     msg.Channels.Title,
			CreatedAt: now,
			UserID:    user.ID,
		}
		if _, err := h.store.InsertMessage(m); err != nil {
			log.Printf("ERROR: inserting KF channels message: %v", err)
		}

	default:
		log.Printf("INFO: unhandled KF message type: %s (msgid=%s)", msg.MsgType, msg.MsgID)
	}
}


// fetchArticleForUser fetches a WeChat article and saves it with user ID.
func (h *WeChatHandler) fetchArticleForUser(url, title, msgID string, now time.Time, userID int64) {
	result, err := h.fetcher.FetchArticle(url, now)
	if err != nil {
		log.Printf("ERROR: fetching article %s: %v", url, err)
		m := &model.Message{
			MsgID:     msgID,
			Type:      "memo",
			Content:   fmt.Sprintf("(article fetch failed: %v) [%s](%s)", err, title, url),
			Title:     title,
			SourceURL: url,
			CreatedAt: now,
			UserID:    userID,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting article error memo: %v", err2)
		}
		return
	}

	m := &model.Message{
		MsgID:     msgID,
		Type:      "article",
		Content:   result.Content,
		Title:     result.Title,
		Filename:  result.Filename,
		SourceURL: url,
		CreatedAt: now,
		UserID:    userID,
	}
	dbID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting article message: %v", err)
		return
	}

	for _, imgFilename := range result.Images {
		att := &model.Attachment{
			MessageID:   dbID,
			Filename:    imgFilename,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting article image attachment %s: %v", imgFilename, err)
		}
	}
}

// fetchGenericOrMemoForUser tries to fetch a URL as an article with user ID; falls back to memo.
func (h *WeChatHandler) fetchGenericOrMemoForUser(url, title, msgID string, now time.Time, userID int64) {
	result, err := h.fetcher.FetchGenericArticle(url, now)
	if err != nil {
		log.Printf("INFO: generic fetch failed for %s: %v, saving as memo", url, err)
		content := fmt.Sprintf("[%s](%s)", title, url)
		m := &model.Message{
			MsgID:     msgID,
			Type:      "memo",
			Content:   content,
			Title:     title,
			SourceURL: url,
			CreatedAt: now,
			UserID:    userID,
		}
		if _, err2 := h.store.InsertMessage(m); err2 != nil {
			log.Printf("ERROR: inserting link memo: %v", err2)
		}
		return
	}

	m := &model.Message{
		MsgID:     msgID,
		Type:      "article",
		Content:   result.Content,
		Title:     result.Title,
		Filename:  result.Filename,
		SourceURL: url,
		CreatedAt: now,
		UserID:    userID,
	}
	dbID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting generic article: %v", err)
		return
	}
	for _, img := range result.Images {
		att := &model.Attachment{
			MessageID:   dbID,
			Filename:    img,
			ContentType: "image/jpeg",
			CreatedAt:   now,
		}
		if _, err := h.store.InsertAttachment(att); err != nil {
			log.Printf("ERROR: inserting generic article image %s: %v", img, err)
		}
	}
	log.Printf("INFO: saved generic article: %s (%d images)", result.Title, len(result.Images))
}

// processKFImageForUser downloads a media file via KF API and saves it as an image message with user ID.
func (h *WeChatHandler) processKFImageForUser(mediaID, datePrefix string, now time.Time, userID int64) {
	data, contentType, err := h.kf.DownloadMedia(mediaID)
	if err != nil {
		log.Printf("ERROR: downloading KF media %s: %v", mediaID, err)
		m := &model.Message{
			Type:      "memo",
			Content:   fmt.Sprintf("(KF image download failed: %v) media_id=%s", err, mediaID),
			CreatedAt: now,
			UserID:    userID,
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
		UserID:    userID,
	}
	msgDBID, err := h.store.InsertMessage(m)
	if err != nil {
		log.Printf("ERROR: inserting KF image message: %v", err)
		return
	}

	att := &model.Attachment{
		MessageID:   msgDBID,
		Filename:    filename,
		ContentType: contentType,
		Size:        int64(len(data)),
		CreatedAt:   now,
	}
	if _, err := h.store.InsertAttachment(att); err != nil {
		log.Printf("ERROR: inserting KF image attachment: %v", err)
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

// truncateStr truncates a string to at most maxRunes Unicode code points, adding "…" if truncated.
func truncateStr(s string, maxRunes int) string {
	// Replace newlines with spaces for single-line display
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// cleanURL strips tracking parameters from a URL.
func cleanURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	// Common tracking params to remove
	for key := range q {
		k := strings.ToLower(key)
		if strings.HasPrefix(k, "utm_") ||
			strings.HasPrefix(k, "share_") ||
			strings.HasPrefix(k, "sharer_") ||
			k == "tt_from" || k == "upstream_biz" || k == "wxshare_count" ||
			k == "req_id_new" || k == "module_name" || k == "category_new" ||
			k == "app" || k == "timestamp" || k == "mpshare" || k == "scene" ||
			k == "srcid" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
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
