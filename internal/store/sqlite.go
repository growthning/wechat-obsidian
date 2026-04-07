package store

import (
	"database/sql"
	"fmt"
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
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode=WAL")
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
	`)
	return err
}

func (s *Store) InsertMessage(msg *model.Message) (int64, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO messages (msg_id, type, content, title, filename, source_url, raw_xml, synced, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.MsgID, msg.Type, msg.Content, msg.Title, msg.Filename, msg.SourceURL,
		msg.RawXML, boolToInt(msg.Synced), msg.CreatedAt,
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

func (s *Store) GetUnsynced(sinceID int64, limit int) ([]model.MessageWithImages, bool, error) {
	rows, err := s.db.Query(
		`SELECT id, type, content, title, filename, source_url, raw_xml, synced, created_at
		 FROM messages
		 WHERE id > ? AND synced = 0
		 ORDER BY id ASC
		 LIMIT ?`,
		sinceID, limit+1,
	)
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

func (s *Store) AckMessages(lastID int64) error {
	_, err := s.db.Exec(`UPDATE messages SET synced = 1 WHERE id <= ?`, lastID)
	return err
}

func (s *Store) ImagePath(filename string) string {
	return filepath.Join(s.dataDir, "images", filename)
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
