package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/garett/aiprod/internal/db"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS files (
		id           TEXT PRIMARY KEY,
		name         TEXT NOT NULL,
		content_hash TEXT NOT NULL,
		mime_type    TEXT DEFAULT '',
		size_bytes   INTEGER NOT NULL,
		tags         TEXT DEFAULT '[]',
		metadata     TEXT DEFAULT '{}',
		created_at   TEXT NOT NULL,
		created_by   TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash);
	CREATE INDEX IF NOT EXISTS idx_files_name ON files(name);

	CREATE TABLE IF NOT EXISTS file_refs (
		file_id  TEXT NOT NULL REFERENCES files(id),
		ref_type TEXT NOT NULL,
		ref_id   TEXT NOT NULL,
		PRIMARY KEY (file_id, ref_type, ref_id)
	);`,
}

type Store struct {
	db      *sql.DB
	baseDir string
}

type File struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	ContentHash string                 `json:"content_hash"`
	MimeType    string                 `json:"mime_type"`
	SizeBytes   int64                  `json:"size_bytes"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	CreatedBy   string                 `json:"created_by"`
}

func NewStore(coreDB *sql.DB, baseDir string) (*Store, error) {
	if err := db.Migrate(coreDB, "storage", migrations); err != nil {
		return nil, fmt.Errorf("migrating storage schema: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0750); err != nil {
		return nil, fmt.Errorf("creating files directory: %w", err)
	}
	return &Store{db: coreDB, baseDir: baseDir}, nil
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// Put stores a file from a reader, content-addressed by SHA-256.
func (s *Store) Put(name, mimeType, createdBy string, tags []string, metadata map[string]interface{}, reader io.Reader) (*File, error) {
	tmpFile, err := os.CreateTemp(s.baseDir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)
	size, err := io.Copy(writer, reader)
	if err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("writing file: %w", err)
	}
	tmpFile.Close()

	hash := hex.EncodeToString(hasher.Sum(nil))

	// Move to content-addressed location
	dir := filepath.Join(s.baseDir, hash[:2])
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating hash dir: %w", err)
	}
	destPath := filepath.Join(dir, hash)
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		if err := os.Rename(tmpPath, destPath); err != nil {
			return nil, fmt.Errorf("moving file: %w", err)
		}
	}

	if tags == nil {
		tags = []string{}
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	id := newID("file_")
	now := time.Now().UTC().Format(time.RFC3339)
	tagsJSON, _ := json.Marshal(tags)
	metaJSON, _ := json.Marshal(metadata)

	_, err = s.db.Exec(
		"INSERT INTO files (id, name, content_hash, mime_type, size_bytes, tags, metadata, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, name, hash, mimeType, size, string(tagsJSON), string(metaJSON), now, createdBy,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting file record: %w", err)
	}

	return &File{
		ID:          id,
		Name:        name,
		ContentHash: hash,
		MimeType:    mimeType,
		SizeBytes:   size,
		Tags:        tags,
		Metadata:    metadata,
		CreatedAt:   now,
		CreatedBy:   createdBy,
	}, nil
}

func (s *Store) Get(id string) (*File, error) {
	f := &File{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		"SELECT id, name, content_hash, mime_type, size_bytes, tags, metadata, created_at, created_by FROM files WHERE id = ?", id,
	).Scan(&f.ID, &f.Name, &f.ContentHash, &f.MimeType, &f.SizeBytes, &tagsJSON, &metaJSON, &f.CreatedAt, &f.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting file: %w", err)
	}
	json.Unmarshal([]byte(tagsJSON), &f.Tags)
	json.Unmarshal([]byte(metaJSON), &f.Metadata)
	return f, nil
}

func (s *Store) Open(id string) (io.ReadCloser, *File, error) {
	f, err := s.Get(id)
	if err != nil || f == nil {
		return nil, nil, err
	}
	path := filepath.Join(s.baseDir, f.ContentHash[:2], f.ContentHash)
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("opening file content: %w", err)
	}
	return file, f, nil
}

type ListOptions struct {
	Tag    string
	Cursor string
	Limit  int
}

func (s *Store) List(opts ListOptions) ([]File, error) {
	query := "SELECT id, name, content_hash, mime_type, size_bytes, tags, metadata, created_at, created_by FROM files WHERE 1=1"
	var args []interface{}

	if opts.Tag != "" {
		query += " AND tags LIKE ?"
		args = append(args, "%\""+opts.Tag+"\"%")
	}
	if opts.Cursor != "" {
		query += " AND created_at < ?"
		args = append(args, opts.Cursor)
	}
	query += " ORDER BY created_at DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		var tagsJSON, metaJSON string
		if err := rows.Scan(&f.ID, &f.Name, &f.ContentHash, &f.MimeType, &f.SizeBytes, &tagsJSON, &metaJSON, &f.CreatedAt, &f.CreatedBy); err != nil {
			return nil, fmt.Errorf("scanning file: %w", err)
		}
		json.Unmarshal([]byte(tagsJSON), &f.Tags)
		json.Unmarshal([]byte(metaJSON), &f.Metadata)
		files = append(files, f)
	}
	return files, rows.Err()
}

func (s *Store) UpdateMeta(id string, tags []string, metadata map[string]interface{}) error {
	setClauses := []string{}
	args := []interface{}{}

	if tags != nil {
		tagsJSON, _ := json.Marshal(tags)
		setClauses = append(setClauses, "tags = ?")
		args = append(args, string(tagsJSON))
	}
	if metadata != nil {
		metaJSON, _ := json.Marshal(metadata)
		setClauses = append(setClauses, "metadata = ?")
		args = append(args, string(metaJSON))
	}
	if len(setClauses) == 0 {
		return nil
	}

	query := "UPDATE files SET "
	for i, c := range setClauses {
		if i > 0 {
			query += ", "
		}
		query += c
	}
	query += " WHERE id = ?"
	args = append(args, id)

	_, err := s.db.Exec(query, args...)
	return err
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM files WHERE id = ?", id)
	return err
}

func (s *Store) AddRef(fileID, refType, refID string) error {
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO file_refs (file_id, ref_type, ref_id) VALUES (?, ?, ?)",
		fileID, refType, refID,
	)
	return err
}
