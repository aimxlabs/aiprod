package email

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/garett/aiprod/internal/db"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS messages (
		id          TEXT PRIMARY KEY,
		message_id  TEXT UNIQUE,
		thread_id   TEXT DEFAULT '',
		from_addr   TEXT NOT NULL,
		to_addrs    TEXT NOT NULL DEFAULT '[]',
		cc_addrs    TEXT DEFAULT '[]',
		bcc_addrs   TEXT DEFAULT '[]',
		subject     TEXT DEFAULT '',
		date        TEXT NOT NULL,
		body_text   TEXT DEFAULT '',
		body_html   TEXT DEFAULT '',
		raw_path    TEXT DEFAULT '',
		size_bytes  INTEGER DEFAULT 0,
		direction   TEXT NOT NULL DEFAULT 'outbound',
		status      TEXT DEFAULT 'received',
		created_at  TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	);

	CREATE TABLE IF NOT EXISTS message_labels (
		message_id TEXT NOT NULL REFERENCES messages(id),
		label      TEXT NOT NULL,
		PRIMARY KEY (message_id, label)
	);

	CREATE TABLE IF NOT EXISTS message_headers (
		message_id TEXT NOT NULL REFERENCES messages(id),
		name       TEXT NOT NULL,
		value      TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS attachments (
		id           TEXT PRIMARY KEY,
		message_id   TEXT NOT NULL REFERENCES messages(id),
		filename     TEXT DEFAULT '',
		content_type TEXT DEFAULT '',
		size_bytes   INTEGER DEFAULT 0,
		file_id      TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS outbound_queue (
		id          TEXT PRIMARY KEY,
		message_id  TEXT NOT NULL REFERENCES messages(id),
		recipient   TEXT NOT NULL,
		attempts    INTEGER DEFAULT 0,
		next_retry  TEXT,
		last_error  TEXT DEFAULT '',
		status      TEXT DEFAULT 'queued'
	);

	CREATE INDEX IF NOT EXISTS idx_msg_thread ON messages(thread_id);
	CREATE INDEX IF NOT EXISTS idx_msg_from ON messages(from_addr);
	CREATE INDEX IF NOT EXISTS idx_msg_date ON messages(date);
	CREATE INDEX IF NOT EXISTS idx_msg_direction ON messages(direction);
	CREATE INDEX IF NOT EXISTS idx_msg_status ON messages(status);
	CREATE INDEX IF NOT EXISTS idx_labels_label ON message_labels(label);
	CREATE INDEX IF NOT EXISTS idx_queue_status ON outbound_queue(status);

	CREATE VIRTUAL TABLE IF NOT EXISTS email_fts USING fts5(
		subject, body_text, from_addr, to_addrs
	);

	CREATE TABLE IF NOT EXISTS email_fts_map (
		msg_id TEXT PRIMARY KEY,
		rowid  INTEGER UNIQUE
	);`,
}

type Store struct {
	db     *sql.DB
	rawDir string
}

type Message struct {
	ID        string   `json:"id"`
	MessageID string   `json:"message_id"`
	ThreadID  string   `json:"thread_id"`
	From      string   `json:"from"`
	To        []string `json:"to"`
	Cc        []string `json:"cc,omitempty"`
	Bcc       []string `json:"bcc,omitempty"`
	Subject   string   `json:"subject"`
	Date      string   `json:"date"`
	BodyText  string   `json:"body_text,omitempty"`
	BodyHTML  string   `json:"body_html,omitempty"`
	RawPath   string   `json:"raw_path,omitempty"`
	SizeBytes int64    `json:"size_bytes"`
	Direction string   `json:"direction"`
	Status    string   `json:"status"`
	Labels    []string `json:"labels,omitempty"`
	CreatedAt string   `json:"created_at"`
}

type QueueEntry struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	Recipient string `json:"recipient"`
	Attempts  int    `json:"attempts"`
	NextRetry string `json:"next_retry"`
	LastError string `json:"last_error"`
	Status    string `json:"status"`
}

func NewStore(emailDB *sql.DB, rawDir string) (*Store, error) {
	if err := db.Migrate(emailDB, "email", migrations); err != nil {
		return nil, fmt.Errorf("migrating email schema: %w", err)
	}
	return &Store{db: emailDB, rawDir: rawDir}, nil
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Store) SaveMessage(msg *Message) error {
	toJSON, _ := json.Marshal(msg.To)
	ccJSON, _ := json.Marshal(msg.Cc)
	bccJSON, _ := json.Marshal(msg.Bcc)

	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages (id, message_id, thread_id, from_addr, to_addrs, cc_addrs, bcc_addrs, subject, date, body_text, body_html, raw_path, size_bytes, direction, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.MessageID, msg.ThreadID, msg.From,
		string(toJSON), string(ccJSON), string(bccJSON),
		msg.Subject, msg.Date, msg.BodyText, msg.BodyHTML,
		msg.RawPath, msg.SizeBytes, msg.Direction, msg.Status,
	)
	if err != nil {
		return fmt.Errorf("saving message: %w", err)
	}

	// Index in FTS
	s.indexFTS(msg.ID, msg.Subject, msg.BodyText, msg.From, string(toJSON))

	return nil
}

func (s *Store) AddLabel(msgID, label string) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO message_labels (message_id, label) VALUES (?, ?)", msgID, label)
	return err
}

func (s *Store) RemoveLabel(msgID, label string) error {
	_, err := s.db.Exec("DELETE FROM message_labels WHERE message_id = ? AND label = ?", msgID, label)
	return err
}

func (s *Store) GetLabels(msgID string) ([]string, error) {
	rows, err := s.db.Query("SELECT label FROM message_labels WHERE message_id = ? ORDER BY label", msgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		rows.Scan(&l)
		labels = append(labels, l)
	}
	return labels, nil
}

func (s *Store) Get(id string) (*Message, error) {
	m := &Message{}
	var toJSON, ccJSON, bccJSON string
	err := s.db.QueryRow(
		`SELECT id, COALESCE(message_id,''), thread_id, from_addr, to_addrs, cc_addrs, bcc_addrs, subject, date, body_text, body_html, raw_path, size_bytes, direction, status, created_at
		 FROM messages WHERE id = ?`, id,
	).Scan(&m.ID, &m.MessageID, &m.ThreadID, &m.From, &toJSON, &ccJSON, &bccJSON,
		&m.Subject, &m.Date, &m.BodyText, &m.BodyHTML, &m.RawPath, &m.SizeBytes,
		&m.Direction, &m.Status, &m.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting message: %w", err)
	}
	json.Unmarshal([]byte(toJSON), &m.To)
	json.Unmarshal([]byte(ccJSON), &m.Cc)
	json.Unmarshal([]byte(bccJSON), &m.Bcc)
	m.Labels, _ = s.GetLabels(id)
	return m, nil
}

type ListOptions struct {
	Label     string
	Direction string
	ThreadID  string
	Cursor    string
	Limit     int
}

func (s *Store) List(opts ListOptions) ([]Message, error) {
	query := `SELECT m.id, COALESCE(m.message_id,''), m.thread_id, m.from_addr, m.to_addrs, m.cc_addrs, m.bcc_addrs, m.subject, m.date, m.size_bytes, m.direction, m.status, m.created_at FROM messages m`
	var args []interface{}
	where := []string{}

	if opts.Label != "" {
		query += " JOIN message_labels ml ON m.id = ml.message_id"
		where = append(where, "ml.label = ?")
		args = append(args, opts.Label)
	}
	if opts.Direction != "" {
		where = append(where, "m.direction = ?")
		args = append(args, opts.Direction)
	}
	if opts.ThreadID != "" {
		where = append(where, "m.thread_id = ?")
		args = append(args, opts.ThreadID)
	}
	if opts.Cursor != "" {
		where = append(where, "m.date < ?")
		args = append(args, opts.Cursor)
	}

	if len(where) > 0 {
		query += " WHERE "
		for i, w := range where {
			if i > 0 {
				query += " AND "
			}
			query += w
		}
	}
	query += " ORDER BY m.date DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var toJSON, ccJSON, bccJSON string
		if err := rows.Scan(&m.ID, &m.MessageID, &m.ThreadID, &m.From, &toJSON, &ccJSON, &bccJSON,
			&m.Subject, &m.Date, &m.SizeBytes, &m.Direction, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(toJSON), &m.To)
		json.Unmarshal([]byte(ccJSON), &m.Cc)
		json.Unmarshal([]byte(bccJSON), &m.Bcc)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (s *Store) Search(query string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT em.msg_id FROM email_fts f
		 JOIN email_fts_map em ON em.rowid = f.rowid
		 WHERE email_fts MATCH ?
		 LIMIT ?`, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching email: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msgID string
		if err := rows.Scan(&msgID); err != nil {
			continue
		}
		m, err := s.Get(msgID)
		if err != nil || m == nil {
			continue
		}
		msgs = append(msgs, *m)
	}
	return msgs, nil
}

func (s *Store) GetThread(threadID string) ([]Message, error) {
	return s.List(ListOptions{ThreadID: threadID})
}

func (s *Store) EnqueueOutbound(msgID string, recipients []string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	for _, rcpt := range recipients {
		id := newID("queue_")
		if _, err := s.db.Exec(
			"INSERT INTO outbound_queue (id, message_id, recipient, next_retry, status) VALUES (?, ?, ?, ?, 'queued')",
			id, msgID, rcpt, now,
		); err != nil {
			return fmt.Errorf("enqueueing %s: %w", rcpt, err)
		}
	}
	return nil
}

func (s *Store) GetPendingQueue(limit int) ([]QueueEntry, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(
		`SELECT id, message_id, recipient, attempts, COALESCE(next_retry,''), COALESCE(last_error,''), status
		 FROM outbound_queue WHERE status = 'queued' AND (next_retry IS NULL OR next_retry <= ?)
		 ORDER BY next_retry LIMIT ?`,
		time.Now().UTC().Format(time.RFC3339), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []QueueEntry
	for rows.Next() {
		var e QueueEntry
		if err := rows.Scan(&e.ID, &e.MessageID, &e.Recipient, &e.Attempts, &e.NextRetry, &e.LastError, &e.Status); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (s *Store) UpdateQueueEntry(id, status, lastError string, nextRetry string) error {
	_, err := s.db.Exec(
		"UPDATE outbound_queue SET status = ?, last_error = ?, next_retry = ?, attempts = attempts + 1 WHERE id = ?",
		status, lastError, nextRetry, id,
	)
	return err
}

func (s *Store) indexFTS(msgID, subject, bodyText, from, toAddrs string) {
	var rowID int64
	err := s.db.QueryRow("SELECT rowid FROM email_fts_map WHERE msg_id = ?", msgID).Scan(&rowID)
	if err == nil {
		s.db.Exec("DELETE FROM email_fts WHERE rowid = ?", rowID)
		s.db.Exec("DELETE FROM email_fts_map WHERE msg_id = ?", msgID)
	}
	res, err := s.db.Exec("INSERT INTO email_fts (subject, body_text, from_addr, to_addrs) VALUES (?, ?, ?, ?)",
		subject, bodyText, from, toAddrs)
	if err == nil {
		newRowID, _ := res.LastInsertId()
		s.db.Exec("INSERT INTO email_fts_map (msg_id, rowid) VALUES (?, ?)", msgID, newRowID)
	}
}
