package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Role represents a chat message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single message in a conversation.
type Message struct {
	ID             int64
	ConversationID string
	Role           Role
	Content        string
	CreatedAt      time.Time
}

// DB wraps a SQLite database for conversation storage.
type DB struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database at the given path and runs
// the schema migration.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

func migrate(db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS messages (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		conversation_id TEXT    NOT NULL,
		role            TEXT    NOT NULL,
		content         TEXT    NOT NULL,
		created_at      TEXT    NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_messages_conversation
		ON messages(conversation_id, id);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("running migration: %w", err)
	}
	return nil
}

// AddMessage inserts a message into a conversation.
func (d *DB) AddMessage(conversationID string, role Role, content string) error {
	_, err := d.db.Exec(
		"INSERT INTO messages (conversation_id, role, content) VALUES (?, ?, ?)",
		conversationID, string(role), content,
	)
	if err != nil {
		return fmt.Errorf("inserting message: %w", err)
	}
	return nil
}

// Messages returns all messages for a conversation, ordered chronologically.
func (d *DB) Messages(conversationID string) ([]Message, error) {
	rows, err := d.db.Query(
		"SELECT id, conversation_id, role, content, created_at FROM messages WHERE conversation_id = ? ORDER BY id",
		conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var createdAt string
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}
		m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// Close closes the database connection.
func (d *DB) Close() error {
	return d.db.Close()
}
