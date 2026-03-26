package docs

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/garett/aiprod/internal/db"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS documents (
		id          TEXT PRIMARY KEY,
		title       TEXT NOT NULL,
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL,
		author      TEXT DEFAULT '',
		tags        TEXT DEFAULT '[]',
		version     INTEGER DEFAULT 1,
		word_count  INTEGER DEFAULT 0,
		dir_path    TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS document_versions (
		id           TEXT PRIMARY KEY,
		doc_id       TEXT NOT NULL REFERENCES documents(id),
		version      INTEGER NOT NULL,
		content_hash TEXT NOT NULL,
		author       TEXT DEFAULT '',
		message      TEXT DEFAULT '',
		created_at   TEXT NOT NULL,
		UNIQUE(doc_id, version)
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
		title, content, content_rowid='rowid'
	);

	CREATE TABLE IF NOT EXISTS docs_fts_map (
		doc_id TEXT PRIMARY KEY,
		rowid  INTEGER UNIQUE
	);`,
}

type Store struct {
	db      *sql.DB
	baseDir string
}

type Document struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	CreatedAt  string   `json:"created_at"`
	ModifiedAt string   `json:"modified_at"`
	Author     string   `json:"author"`
	Tags       []string `json:"tags"`
	Version    int      `json:"version"`
	WordCount  int      `json:"word_count"`
}

type Version struct {
	ID          string `json:"id"`
	DocID       string `json:"doc_id"`
	Version     int    `json:"version"`
	ContentHash string `json:"content_hash"`
	Author      string `json:"author"`
	Message     string `json:"message"`
	CreatedAt   string `json:"created_at"`
}

func NewStore(coreDB *sql.DB, baseDir string) (*Store, error) {
	if err := db.Migrate(coreDB, "docs", migrations); err != nil {
		return nil, fmt.Errorf("migrating docs schema: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return nil, fmt.Errorf("creating docs directory: %w", err)
	}
	return &Store{db: coreDB, baseDir: baseDir}, nil
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Store) Create(title, author string, tags []string, content string) (*Document, error) {
	id := newID("doc_")
	now := time.Now().UTC().Format(time.RFC3339)

	if tags == nil {
		tags = []string{}
	}

	// Create document directory
	docDir := filepath.Join(s.baseDir, id)
	versionsDir := filepath.Join(docDir, "versions")
	if err := os.MkdirAll(versionsDir, 0750); err != nil {
		return nil, fmt.Errorf("creating doc directory: %w", err)
	}

	// Write content
	currentPath := filepath.Join(docDir, "current.md")
	if err := os.WriteFile(currentPath, []byte(content), 0640); err != nil {
		return nil, fmt.Errorf("writing doc content: %w", err)
	}

	// Write first version
	versionPath := filepath.Join(versionsDir, "0001.md")
	if err := os.WriteFile(versionPath, []byte(content), 0640); err != nil {
		return nil, fmt.Errorf("writing version: %w", err)
	}

	wc := wordCount(content)
	hash := contentHash(content)
	tagsJSON, _ := json.Marshal(tags)

	// Insert document record
	_, err := s.db.Exec(
		"INSERT INTO documents (id, title, created_at, modified_at, author, tags, version, word_count, dir_path) VALUES (?, ?, ?, ?, ?, ?, 1, ?, ?)",
		id, title, now, now, author, string(tagsJSON), wc, docDir,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting document: %w", err)
	}

	// Insert version record
	_, err = s.db.Exec(
		"INSERT INTO document_versions (id, doc_id, version, content_hash, author, message, created_at) VALUES (?, ?, 1, ?, ?, 'Initial version', ?)",
		newID("ver_"), id, hash, author, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting version: %w", err)
	}

	// Index in FTS
	s.indexFTS(id, title, content)

	return &Document{
		ID: id, Title: title, CreatedAt: now, ModifiedAt: now,
		Author: author, Tags: tags, Version: 1, WordCount: wc,
	}, nil
}

func (s *Store) Get(id string) (*Document, error) {
	d := &Document{}
	var tagsJSON string
	err := s.db.QueryRow(
		"SELECT id, title, created_at, modified_at, author, tags, version, word_count FROM documents WHERE id = ?", id,
	).Scan(&d.ID, &d.Title, &d.CreatedAt, &d.ModifiedAt, &d.Author, &tagsJSON, &d.Version, &d.WordCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting document: %w", err)
	}
	json.Unmarshal([]byte(tagsJSON), &d.Tags)
	return d, nil
}

func (s *Store) ReadContent(id string) (string, error) {
	d, err := s.Get(id)
	if err != nil || d == nil {
		return "", err
	}
	docDir := filepath.Join(s.baseDir, id)
	data, err := os.ReadFile(filepath.Join(docDir, "current.md"))
	if err != nil {
		return "", fmt.Errorf("reading document content: %w", err)
	}
	return string(data), nil
}

func (s *Store) ReadVersion(id string, version int) (string, error) {
	docDir := filepath.Join(s.baseDir, id)
	path := filepath.Join(docDir, "versions", fmt.Sprintf("%04d.md", version))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading version %d: %w", version, err)
	}
	return string(data), nil
}

func (s *Store) Update(id, author, message, content string) (*Document, error) {
	d, err := s.Get(id)
	if err != nil || d == nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	newVersion := d.Version + 1
	wc := wordCount(content)
	hash := contentHash(content)

	// Write new current
	docDir := filepath.Join(s.baseDir, id)
	if err := os.WriteFile(filepath.Join(docDir, "current.md"), []byte(content), 0640); err != nil {
		return nil, fmt.Errorf("writing content: %w", err)
	}

	// Write version snapshot
	versionPath := filepath.Join(docDir, "versions", fmt.Sprintf("%04d.md", newVersion))
	if err := os.WriteFile(versionPath, []byte(content), 0640); err != nil {
		return nil, fmt.Errorf("writing version: %w", err)
	}

	// Update document record
	_, err = s.db.Exec(
		"UPDATE documents SET modified_at = ?, version = ?, word_count = ? WHERE id = ?",
		now, newVersion, wc, id,
	)
	if err != nil {
		return nil, fmt.Errorf("updating document: %w", err)
	}

	// Insert version record
	if message == "" {
		message = fmt.Sprintf("Version %d", newVersion)
	}
	_, err = s.db.Exec(
		"INSERT INTO document_versions (id, doc_id, version, content_hash, author, message, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		newID("ver_"), id, newVersion, hash, author, message, now,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting version: %w", err)
	}

	// Re-index
	s.indexFTS(id, d.Title, content)

	d.ModifiedAt = now
	d.Version = newVersion
	d.WordCount = wc
	return d, nil
}

func (s *Store) ListVersions(id string) ([]Version, error) {
	rows, err := s.db.Query(
		"SELECT id, doc_id, version, content_hash, author, message, created_at FROM document_versions WHERE doc_id = ? ORDER BY version", id,
	)
	if err != nil {
		return nil, fmt.Errorf("listing versions: %w", err)
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.DocID, &v.Version, &v.ContentHash, &v.Author, &v.Message, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning version: %w", err)
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

type ListOptions struct {
	Tag    string
	Author string
	Cursor string
	Limit  int
}

func (s *Store) List(opts ListOptions) ([]Document, error) {
	query := "SELECT id, title, created_at, modified_at, author, tags, version, word_count FROM documents WHERE 1=1"
	var args []interface{}

	if opts.Tag != "" {
		query += " AND tags LIKE ?"
		args = append(args, "%\""+opts.Tag+"\"%")
	}
	if opts.Author != "" {
		query += " AND author = ?"
		args = append(args, opts.Author)
	}
	if opts.Cursor != "" {
		query += " AND modified_at < ?"
		args = append(args, opts.Cursor)
	}
	query += " ORDER BY modified_at DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		var tagsJSON string
		if err := rows.Scan(&d.ID, &d.Title, &d.CreatedAt, &d.ModifiedAt, &d.Author, &tagsJSON, &d.Version, &d.WordCount); err != nil {
			return nil, fmt.Errorf("scanning document: %w", err)
		}
		json.Unmarshal([]byte(tagsJSON), &d.Tags)
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *Store) Delete(id string) error {
	docDir := filepath.Join(s.baseDir, id)
	os.RemoveAll(docDir)
	s.db.Exec("DELETE FROM document_versions WHERE doc_id = ?", id)
	_, err := s.db.Exec("DELETE FROM documents WHERE id = ?", id)
	return err
}

func (s *Store) Search(query string, limit int) ([]Document, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT dm.doc_id FROM docs_fts f
		 JOIN docs_fts_map dm ON dm.rowid = f.rowid
		 WHERE docs_fts MATCH ?
		 LIMIT ?`, query, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("searching docs: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var docID string
		if err := rows.Scan(&docID); err != nil {
			continue
		}
		d, err := s.Get(docID)
		if err != nil || d == nil {
			continue
		}
		docs = append(docs, *d)
	}
	return docs, nil
}

func (s *Store) indexFTS(docID, title, content string) {
	// Upsert into FTS
	var rowID int64
	err := s.db.QueryRow("SELECT rowid FROM docs_fts_map WHERE doc_id = ?", docID).Scan(&rowID)
	if err == nil {
		s.db.Exec("DELETE FROM docs_fts WHERE rowid = ?", rowID)
		s.db.Exec("DELETE FROM docs_fts_map WHERE doc_id = ?", docID)
	}
	res, err := s.db.Exec("INSERT INTO docs_fts (title, content) VALUES (?, ?)", title, content)
	if err == nil {
		newRowID, _ := res.LastInsertId()
		s.db.Exec("INSERT INTO docs_fts_map (doc_id, rowid) VALUES (?, ?)", docID, newRowID)
	}
}

func wordCount(s string) int {
	return len(strings.Fields(s))
}

func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
