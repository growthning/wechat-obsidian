package wechat

import (
	"encoding/xml"
	"fmt"
)

// CallbackEncrypt is the outer envelope for encrypted Enterprise WeChat callbacks.
type CallbackEncrypt struct {
	XMLName    xml.Name `xml:"xml"`
	ToUserName string   `xml:"ToUserName"`
	AgentID    string   `xml:"AgentID"`
	Encrypt    string   `xml:"Encrypt"`
}

// IncomingMessage represents a decrypted Enterprise WeChat message.
// Fields are populated depending on MsgType (text, image, link, file).
type IncomingMessage struct {
	XMLName         xml.Name `xml:"xml"`
	ToUserName      string   `xml:"ToUserName"`
	FromUserName    string   `xml:"FromUserName"`
	CreateTime      int64    `xml:"CreateTime"`
	MsgType         string   `xml:"MsgType"`
	MsgID           string   `xml:"MsgId"`
	Content         string   `xml:"Content"`     // text
	PicURL          string   `xml:"PicUrl"`      // image
	MediaID         string   `xml:"MediaId"`     // image
	LinkTitle       string   `xml:"Title"`       // link
	LinkDescription string   `xml:"Description"` // link
	LinkURL         string   `xml:"Url"`         // link
	FileName        string   `xml:"FileName"`    // file
	Event           string   `xml:"Event"`       // event type (e.g. kf_msg_or_event)
	Token           string   `xml:"Token"`       // KF callback token for sync_msg
	OpenKFID        string   `xml:"OpenKfId"`    // KF account ID
}

// ParseCallback parses the encrypted callback envelope XML.
func ParseCallback(data []byte) (*CallbackEncrypt, error) {
	var cb CallbackEncrypt
	if err := xml.Unmarshal(data, &cb); err != nil {
		return nil, fmt.Errorf("failed to parse callback XML: %w", err)
	}
	return &cb, nil
}

// ParseMessage parses a decrypted incoming message XML.
func ParseMessage(data []byte) (*IncomingMessage, error) {
	var msg IncomingMessage
	if err := xml.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to parse message XML: %w", err)
	}
	return &msg, nil
}
