package wechat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// KFClient is a WeChat Customer Service (微信客服) API client.
// It manages access_token lifecycle and provides methods to sync messages and download media.
type KFClient struct {
	corpID      string
	secret      string
	accessToken string
	tokenExpiry time.Time
	mu          sync.Mutex
	client      *http.Client
}

// NewKFClient creates a new KFClient.
// secret should be kf_secret if set, otherwise the regular app secret.
func NewKFClient(corpID, secret string) *KFClient {
	return &KFClient{
		corpID: corpID,
		secret: secret,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// accessTokenResponse is the response from the gettoken API.
type accessTokenResponse struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// GetAccessToken returns a cached access token or fetches a new one if expired.
func (k *KFClient) GetAccessToken() (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	// Return cached token if still valid (with 5 minute buffer)
	if k.accessToken != "" && time.Now().Add(5*time.Minute).Before(k.tokenExpiry) {
		return k.accessToken, nil
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/gettoken?corpid=%s&corpsecret=%s", k.corpID, k.secret)
	resp, err := k.client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching access token: %w", err)
	}
	defer resp.Body.Close()

	var result accessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding access token response: %w", err)
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("access token API error: %d %s", result.ErrCode, result.ErrMsg)
	}

	k.accessToken = result.AccessToken
	k.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	return k.accessToken, nil
}

// KFSyncResponse is the response from the sync_msg API.
type KFSyncResponse struct {
	ErrCode    int         `json:"errcode"`
	ErrMsg     string      `json:"errmsg"`
	NextCursor string      `json:"next_cursor"`
	HasMore    int         `json:"has_more"`
	MsgList    []KFMessage `json:"msg_list"`
}

// KFMessage represents a single message from the customer service sync API.
type KFMessage struct {
	MsgID          string `json:"msgid"`
	OpenKFID       string `json:"open_kfid"`
	ExternalUserID string `json:"external_userid"`
	SendTime       int64  `json:"send_time"`
	Origin         int    `json:"origin"` // 3=微信客户发送, 4=系统, 5=接待人员
	MsgType        string `json:"msgtype"`

	Text     *KFTextMsg     `json:"text,omitempty"`
	Image    *KFImageMsg    `json:"image,omitempty"`
	Link     *KFLinkMsg     `json:"link,omitempty"`
	File     *KFFileMsg     `json:"file,omitempty"`
	Channels *KFChannelsMsg `json:"channels,omitempty"`
	Event    *KFEventMsg    `json:"event,omitempty"`
}

// KFEventMsg is the event content of a KF message.
type KFEventMsg struct {
	EventType   string `json:"event_type"`
	OpenKFID    string `json:"open_kfid"`
	ExternalUID string `json:"external_userid"`
	WelcomeCode string `json:"welcome_code"`
}

// KFTextMsg is the text content of a KF message.
type KFTextMsg struct {
	Content string `json:"content"`
}

// KFImageMsg is the image content of a KF message.
type KFImageMsg struct {
	MediaID string `json:"media_id"`
}

// KFLinkMsg is the link content of a KF message.
type KFLinkMsg struct {
	Title  string `json:"title"`
	Desc   string `json:"desc"`
	URL    string `json:"url"`
	PicURL string `json:"pic_url"`
}

// KFFileMsg is the file content of a KF message.
type KFFileMsg struct {
	MediaID string `json:"media_id"`
}

// KFChannelsMsg is the channels (视频号) content of a KF message.
type KFChannelsMsg struct {
	SubType  int    `json:"sub_type"`  // 1=视频, 2=直播, etc.
	Nickname string `json:"nickname"`  // 视频号名称
	Title    string `json:"title"`     // 标题
}

// syncMsgRequest is the request body for the sync_msg API.
type syncMsgRequest struct {
	Cursor   string `json:"cursor"`
	Token    string `json:"token"`
	Limit    int    `json:"limit"`
	OpenKFID string `json:"open_kfid,omitempty"`
}

// SyncMessages fetches messages from the customer service sync_msg API.
// callbackToken is the Token from the callback event XML, cursor is for pagination.
func (k *KFClient) SyncMessages(callbackToken, cursor, openKFID string) (*KFSyncResponse, error) {
	token, err := k.GetAccessToken()
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	reqBody := syncMsgRequest{
		Cursor:   cursor,
		Token:    callbackToken,
		Limit:    1000,
		OpenKFID: openKFID,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling sync_msg request: %w", err)
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/kf/sync_msg?access_token=%s", token)
	resp, err := k.client.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("calling sync_msg API: %w", err)
	}
	defer resp.Body.Close()

	var result KFSyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding sync_msg response: %w", err)
	}

	if result.ErrCode != 0 {
		return nil, fmt.Errorf("sync_msg API error: %d %s", result.ErrCode, result.ErrMsg)
	}

	return &result, nil
}

// SendTextMessage sends a text message to a user via KF API.
// It first transfers the session to the service state (API-handled) to enable sending.
func (k *KFClient) SendTextMessage(openKFID, externalUserID, content string) error {
	token, err := k.GetAccessToken()
	if err != nil {
		return fmt.Errorf("getting access token: %w", err)
	}

	// Transfer session to AI assistant state (service_state=1) to enable sending
	transBody, _ := json.Marshal(map[string]interface{}{
		"open_kfid":       openKFID,
		"external_userid": externalUserID,
		"service_state":   1,
	})
	transURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/kf/service_state/trans?access_token=%s", token)
	transResp, err := k.client.Post(transURL, "application/json", bytes.NewReader(transBody))
	if err != nil {
		return fmt.Errorf("transferring session: %w", err)
	}
	var transResult struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	json.NewDecoder(transResp.Body).Decode(&transResult)
	transResp.Body.Close()
	if transResult.ErrCode != 0 {
		fmt.Printf("WARN: session trans: %d %s\n", transResult.ErrCode, transResult.ErrMsg)
	}

	body := map[string]interface{}{
		"touser":    externalUserID,
		"open_kfid": openKFID,
		"msgtype":   "text",
		"text": map[string]string{
			"content": content,
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling send_msg request: %w", err)
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/kf/send_msg?access_token=%s", token)
	resp, err := k.client.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("calling send_msg API: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding send_msg response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("send_msg API error: %d %s", result.ErrCode, result.ErrMsg)
	}

	return nil
}

// SendMsgOnEvent sends a message using a welcome_code from an enter_session event.
func (k *KFClient) SendMsgOnEvent(welcomeCode, content string) error {
	token, err := k.GetAccessToken()
	if err != nil {
		return fmt.Errorf("getting access token: %w", err)
	}

	body := map[string]interface{}{
		"code":    welcomeCode,
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/kf/send_msg_on_event?access_token=%s", token)
	resp, err := k.client.Post(url, "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("calling send_msg_on_event: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if result.ErrCode != 0 {
		return fmt.Errorf("send_msg_on_event error: %d %s", result.ErrCode, result.ErrMsg)
	}
	return nil
}

// DownloadMedia downloads a media file by media_id.
// Returns the file data, content-type, and any error.
func (k *KFClient) DownloadMedia(mediaID string) ([]byte, string, error) {
	token, err := k.GetAccessToken()
	if err != nil {
		return nil, "", fmt.Errorf("getting access token: %w", err)
	}

	url := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/media/get?access_token=%s&media_id=%s", token, mediaID)
	resp, err := k.client.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("downloading media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("media download unexpected status: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("reading media body: %w", err)
	}

	contentType := resp.Header.Get("Content-Type")

	// Check if the response is a JSON error instead of actual media
	if contentType == "application/json" || contentType == "text/plain" {
		var errResp struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if json.Unmarshal(data, &errResp) == nil && errResp.ErrCode != 0 {
			return nil, "", fmt.Errorf("media API error: %d %s", errResp.ErrCode, errResp.ErrMsg)
		}
	}

	return data, contentType, nil
}
