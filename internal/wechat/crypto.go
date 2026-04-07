package wechat

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// VerifySignature verifies the signature for an encrypted callback message.
// It sorts [token, timestamp, nonce, encrypt], joins them, SHA1 hashes, and compares to signature.
func VerifySignature(token, timestamp, nonce, encrypt, signature string) bool {
	parts := []string{token, timestamp, nonce, encrypt}
	sort.Strings(parts)
	joined := strings.Join(parts, "")
	h := sha1.New()
	h.Write([]byte(joined))
	computed := fmt.Sprintf("%x", h.Sum(nil))
	return computed == signature
}

// VerifyURLSignature verifies the signature for URL verification (no encrypt field).
// It sorts [token, timestamp, nonce], joins them, SHA1 hashes, and compares to signature.
func VerifyURLSignature(token, timestamp, nonce, signature string) bool {
	parts := []string{token, timestamp, nonce}
	sort.Strings(parts)
	joined := strings.Join(parts, "")
	h := sha1.New()
	h.Write([]byte(joined))
	computed := fmt.Sprintf("%x", h.Sum(nil))
	return computed == signature
}

// DecryptMessage decrypts an Enterprise WeChat AES-256-CBC encrypted message.
// Returns the message bytes, the corpID, and any error.
func DecryptMessage(encodingAESKey, encrypted string) ([]byte, string, error) {
	// Decode AES key: base64(encodingAESKey + "=")
	aesKey, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode AES key: %w", err)
	}
	if len(aesKey) != 32 {
		return nil, "", fmt.Errorf("invalid AES key length: expected 32, got %d", len(aesKey))
	}

	// Decode ciphertext
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	// AES-256-CBC decrypt with IV = aesKey[:16]
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create AES cipher: %w", err)
	}

	if len(ciphertext) < aes.BlockSize {
		return nil, "", errors.New("ciphertext too short")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, "", errors.New("ciphertext is not a multiple of block size")
	}

	iv := aesKey[:16]
	mode := cipher.NewCBCDecrypter(block, iv)
	plaintext := make([]byte, len(ciphertext))
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS#7 padding
	plaintext, err = pkcs7Unpad(plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("failed to remove padding: %w", err)
	}

	// Parse plaintext: 16 bytes random + 4 bytes msg_len (big endian) + msg + corpID
	if len(plaintext) < 20 {
		return nil, "", errors.New("plaintext too short to parse")
	}

	msgLen := int(binary.BigEndian.Uint32(plaintext[16:20]))
	if len(plaintext) < 20+msgLen {
		return nil, "", errors.New("plaintext too short for declared message length")
	}

	msg := plaintext[20 : 20+msgLen]
	corpID := string(plaintext[20+msgLen:])

	return msg, corpID, nil
}

// pkcs7Unpad removes PKCS#7 padding from data.
func pkcs7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, errors.New("empty data")
	}
	padding := int(data[length-1])
	// WeChat uses PKCS#7 with 32-byte block size, not AES's 16-byte block size
	if padding == 0 || padding > 32 {
		return nil, fmt.Errorf("invalid padding size: %d", padding)
	}
	if padding > length {
		return nil, errors.New("padding size larger than data")
	}
	// Verify all padding bytes
	for i := length - padding; i < length; i++ {
		if data[i] != byte(padding) {
			return nil, errors.New("invalid padding bytes")
		}
	}
	return data[:length-padding], nil
}
