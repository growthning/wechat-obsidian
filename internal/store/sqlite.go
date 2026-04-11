package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/user/wechat-obsidian/internal/model"
)

type Store struct {
	db      *sql.DB
	dataDir string
}

func New(dataDir string) (*Store, error) {
	imagesDir := filepath.Join(dataDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "wechat.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode=WAL&_pragma=busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	s := &Store{db: db, dataDir: dataDir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			msg_id     TEXT    NOT NULL DEFAULT '',
			type       TEXT    NOT NULL,
			content    TEXT    NOT NULL DEFAULT '',
			title      TEXT    NOT NULL DEFAULT '',
			filename   TEXT    NOT NULL DEFAULT '',
			source_url TEXT    NOT NULL DEFAULT '',
			raw_xml    TEXT    NOT NULL DEFAULT '',
			synced     INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_messages_msg_id ON messages(msg_id) WHERE msg_id != '';

		CREATE TABLE IF NOT EXISTS attachments (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id   INTEGER NOT NULL,
			filename     TEXT    NOT NULL,
			original_url TEXT    NOT NULL DEFAULT '',
			content_type TEXT    NOT NULL DEFAULT '',
			size         INTEGER NOT NULL DEFAULT 0,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (message_id) REFERENCES messages(id)
		);

		CREATE TABLE IF NOT EXISTS users (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			external_userid TEXT NOT NULL UNIQUE,
			api_key         TEXT NOT NULL UNIQUE,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		return err
	}

	// Add user_id column to messages (ignore error if already exists)
	s.db.Exec(`ALTER TABLE messages ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_user_id ON messages(user_id)`)

	// KV store for persistent state (e.g. KF sync cursor)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)

	// Device-independent sync tracking
	s.db.Exec(`CREATE TABLE IF NOT EXISTS device_acks (
		device_id TEXT NOT NULL,
		user_id INTEGER NOT NULL,
		last_acked_id INTEGER NOT NULL DEFAULT 0,
		last_acked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (device_id, user_id)
	)`)

	return nil
}

// GetKV retrieves a value by key from the kv store.
func (s *Store) GetKV(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetKV sets a key-value pair in the kv store (upsert).
func (s *Store) SetKV(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?`, key, value, value)
	return err
}

// MessageExists checks if a message with the given msg_id already exists.
func (s *Store) MessageExists(msgID string) bool {
	if msgID == "" {
		return false
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE msg_id = ?`, msgID).Scan(&count)
	return count > 0
}

// ArticleExistsByURL checks if an article with the given source_url already exists.
func (s *Store) ArticleExistsByURL(url string) bool {
	if url == "" {
		return false
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE source_url = ? AND type = 'article'`, url).Scan(&count)
	return count > 0
}

func (s *Store) InsertMessage(msg *model.Message) (int64, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO messages (msg_id, type, content, title, filename, source_url, raw_xml, synced, created_at, user_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.MsgID, msg.Type, msg.Content, msg.Title, msg.Filename, msg.SourceURL,
		msg.RawXML, boolToInt(msg.Synced), msg.CreatedAt, msg.UserID,
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// Duplicate msg_id, skip
		return 0, nil
	}
	return id, nil
}

func (s *Store) InsertAttachment(att *model.Attachment) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO attachments (message_id, filename, original_url, content_type, size, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		att.MessageID, att.Filename, att.OriginalURL, att.ContentType, att.Size, att.CreatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetUnsynced(sinceID int64, limit int, userID int64) ([]model.MessageWithImages, bool, error) {
	query := `SELECT id, type, content, title, filename, source_url, raw_xml, synced, created_at
		 FROM messages
		 WHERE id > ? AND synced = 0`
	args := []interface{}{sinceID}
	if userID > 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var msgs []model.MessageWithImages
	for rows.Next() {
		var m model.Message
		var synced int
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &m.Title, &m.Filename,
			&m.SourceURL, &m.RawXML, &synced, &m.CreatedAt,
		); err != nil {
			return nil, false, err
		}
		m.Synced = synced != 0
		msgs = append(msgs, model.MessageWithImages{Message: m})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}

	// Load attachment filenames for each message
	for i := range msgs {
		images, err := s.getAttachmentFilenames(msgs[i].ID)
		if err != nil {
			return nil, false, err
		}
		msgs[i].Images = images
	}

	return msgs, hasMore, nil
}

func (s *Store) getAttachmentFilenames(messageID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT filename FROM attachments WHERE message_id = ? ORDER BY id ASC`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var filenames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		filenames = append(filenames, name)
	}
	return filenames, rows.Err()
}

func (s *Store) AckMessages(lastID int64, userID int64) error {
	if userID > 0 {
		_, err := s.db.Exec(`UPDATE messages SET synced = 1 WHERE id <= ? AND user_id = ?`, lastID, userID)
		return err
	}
	_, err := s.db.Exec(`UPDATE messages SET synced = 1 WHERE id <= ?`, lastID)
	return err
}

// GetUnsyncedForDevice returns messages where id > device's last_acked_id.
func (s *Store) GetUnsyncedForDevice(deviceID string, userID int64, limit int) ([]model.MessageWithImages, bool, error) {
	// Get the device's last acked id
	var lastAckedID int64
	err := s.db.QueryRow(
		`SELECT last_acked_id FROM device_acks WHERE device_id = ? AND user_id = ?`,
		deviceID, userID,
	).Scan(&lastAckedID)
	if err == sql.ErrNoRows {
		lastAckedID = 0
	} else if err != nil {
		return nil, false, err
	}

	query := `SELECT id, type, content, title, filename, source_url, raw_xml, synced, created_at
		 FROM messages
		 WHERE id > ?`
	args := []interface{}{lastAckedID}
	if userID > 0 {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	query += ` ORDER BY id ASC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var msgs []model.MessageWithImages
	for rows.Next() {
		var m model.Message
		var synced int
		if err := rows.Scan(
			&m.ID, &m.Type, &m.Content, &m.Title, &m.Filename,
			&m.SourceURL, &m.RawXML, &synced, &m.CreatedAt,
		); err != nil {
			return nil, false, err
		}
		m.Synced = synced != 0
		msgs = append(msgs, model.MessageWithImages{Message: m})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(msgs) > limit
	if hasMore {
		msgs = msgs[:limit]
	}

	// Load attachment filenames for each message
	for i := range msgs {
		images, err := s.getAttachmentFilenames(msgs[i].ID)
		if err != nil {
			return nil, false, err
		}
		msgs[i].Images = images
	}

	return msgs, hasMore, nil
}

// AckMessagesForDevice updates the device_acks table for a specific device.
func (s *Store) AckMessagesForDevice(deviceID string, lastID int64, userID int64) error {
	_, err := s.db.Exec(
		`INSERT INTO device_acks (device_id, user_id, last_acked_id, last_acked_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(device_id, user_id) DO UPDATE SET
		   last_acked_id = MAX(last_acked_id, ?),
		   last_acked_at = CURRENT_TIMESTAMP`,
		deviceID, userID, lastID, lastID,
	)
	return err
}

func (s *Store) CleanupSynced(retentionDays int) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// 1. Query attachment filenames for messages to delete
	rows, err := tx.Query(
		`SELECT a.filename FROM attachments a
		 JOIN messages m ON a.message_id = m.id
		 WHERE m.synced = 1 AND m.created_at < datetime('now', '-' || CAST(? AS TEXT) || ' days')`,
		retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("query attachments: %w", err)
	}
	var filenames []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan attachment: %w", err)
		}
		filenames = append(filenames, f)
	}
	rows.Close()

	// 1b. Query video filenames for video messages to delete
	videoRows, err := tx.Query(
		`SELECT filename FROM messages
		 WHERE type = 'video' AND filename != '' AND synced = 1
		 AND created_at < datetime('now', '-' || CAST(? AS TEXT) || ' days')`,
		retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("query video filenames: %w", err)
	}
	var videoFilenames []string
	for videoRows.Next() {
		var f string
		if err := videoRows.Scan(&f); err != nil {
			videoRows.Close()
			return 0, fmt.Errorf("scan video filename: %w", err)
		}
		videoFilenames = append(videoFilenames, f)
	}
	videoRows.Close()

	// 2. Delete attachments
	_, err = tx.Exec(
		`DELETE FROM attachments WHERE message_id IN (
			SELECT id FROM messages WHERE synced = 1 AND created_at < datetime('now', '-' || CAST(? AS TEXT) || ' days')
		)`, retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("delete attachments: %w", err)
	}

	// 3. Delete messages
	res, err := tx.Exec(
		`DELETE FROM messages WHERE synced = 1 AND created_at < datetime('now', '-' || CAST(? AS TEXT) || ' days')`,
		retentionDays,
	)
	if err != nil {
		return 0, fmt.Errorf("delete messages: %w", err)
	}
	count, _ := res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}

	// 4. Delete image files from disk (best-effort)
	for _, fname := range filenames {
		path := s.ImagePath(fname)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("WARN: cleanup failed to remove image %s: %v", path, err)
		}
	}

	// 5. Delete video files from disk (best-effort)
	for _, fname := range videoFilenames {
		path := filepath.Join(s.dataDir, "videos", fname)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("WARN: cleanup failed to remove video %s: %v", path, err)
		}
	}

	return int(count), nil
}

func (s *Store) DataDir() string {
	return s.dataDir
}

func (s *Store) ImagePath(filename string) string {
	return filepath.Join(s.dataDir, "images", filename)
}

func (s *Store) CreateUser(externalUserID string) (*model.User, error) {
	// Generate 32-byte hex API key
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}
	apiKey := hex.EncodeToString(b)

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO users (external_userid, api_key) VALUES (?, ?)`,
		externalUserID, apiKey,
	)
	if err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	// Return the user (handles both new and existing)
	return s.GetUserByExternalID(externalUserID)
}

func (s *Store) GetUserByAPIKey(apiKey string) (*model.User, error) {
	var u model.User
	err := s.db.QueryRow(
		`SELECT id, external_userid, api_key, created_at FROM users WHERE api_key = ?`,
		apiKey,
	).Scan(&u.ID, &u.ExternalUserID, &u.APIKey, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) GetUserByExternalID(externalUserID string) (*model.User, error) {
	var u model.User
	err := s.db.QueryRow(
		`SELECT id, external_userid, api_key, created_at FROM users WHERE external_userid = ?`,
		externalUserID,
	).Scan(&u.ID, &u.ExternalUserID, &u.APIKey, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT id, external_userid, api_key, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.ExternalUserID, &u.APIKey, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) Close() error {
	return s.db.Close()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
