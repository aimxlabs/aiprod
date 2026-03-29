package memory

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/garett/aiprod/internal/db"
	"github.com/garett/aiprod/internal/llm"
)

var migrations = []string{
	// v1: initial schema
	`CREATE TABLE IF NOT EXISTS memories (
		id               TEXT PRIMARY KEY,
		agent_id         TEXT NOT NULL,
		namespace        TEXT DEFAULT 'default',
		key              TEXT NOT NULL,
		content          TEXT NOT NULL,
		content_type     TEXT DEFAULT 'text',
		source_type      TEXT DEFAULT '',
		source_id        TEXT DEFAULT '',
		importance       REAL DEFAULT 0.5,
		access_count     INTEGER DEFAULT 0,
		tags             TEXT DEFAULT '[]',
		metadata         TEXT DEFAULT '{}',
		created_at       TEXT NOT NULL,
		modified_at      TEXT NOT NULL,
		last_accessed_at TEXT DEFAULT '',
		expires_at       TEXT DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_mem_agent ON memories(agent_id);
	CREATE INDEX IF NOT EXISTS idx_mem_ns ON memories(agent_id, namespace);
	CREATE INDEX IF NOT EXISTS idx_mem_key ON memories(agent_id, namespace, key);
	CREATE INDEX IF NOT EXISTS idx_mem_importance ON memories(importance DESC);
	CREATE INDEX IF NOT EXISTS idx_mem_expires ON memories(expires_at) WHERE expires_at != '';

	CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(key, content, tags);
	CREATE TABLE IF NOT EXISTS memories_fts_map (memory_id TEXT PRIMARY KEY, rowid INTEGER UNIQUE);

	CREATE TABLE IF NOT EXISTS scratchpad (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		session_id  TEXT DEFAULT '',
		key         TEXT NOT NULL,
		value       TEXT NOT NULL,
		ttl_seconds INTEGER DEFAULT 3600,
		created_at  TEXT NOT NULL,
		expires_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_scratch_agent ON scratchpad(agent_id);
	CREATE INDEX IF NOT EXISTS idx_scratch_expires ON scratchpad(expires_at);

	CREATE TABLE IF NOT EXISTS compressions (
		id                TEXT PRIMARY KEY,
		agent_id          TEXT NOT NULL,
		source_type       TEXT NOT NULL,
		source_id         TEXT DEFAULT '',
		original_tokens   INTEGER DEFAULT 0,
		compressed_tokens INTEGER DEFAULT 0,
		original_hash     TEXT NOT NULL,
		summary           TEXT NOT NULL,
		kept_keys         TEXT DEFAULT '[]',
		dropped_keys      TEXT DEFAULT '[]',
		compression_ratio REAL DEFAULT 0.0,
		method            TEXT DEFAULT 'extractive',
		metadata          TEXT DEFAULT '{}',
		created_at        TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_comp_agent ON compressions(agent_id);`,

	// v2: add unique constraint for upsert, add agent_id to FTS
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_upsert ON memories(agent_id, namespace, key);

	DROP TABLE IF EXISTS memories_fts;
	DROP TABLE IF EXISTS memories_fts_map;
	CREATE VIRTUAL TABLE memories_fts USING fts5(agent_id, key, content, tags);
	CREATE TABLE memories_fts_map (memory_id TEXT PRIMARY KEY, rowid INTEGER UNIQUE);

	INSERT INTO memories_fts (agent_id, key, content, tags)
		SELECT agent_id, key, content, tags FROM memories;`,

	// v3: add embedding column for semantic search
	`ALTER TABLE memories ADD COLUMN embedding TEXT DEFAULT '';`,
}

type Store struct {
	db       *sql.DB
	llm      *llm.Client  // optional — enables embedding generation
	vecIndex *VecIndex     // in-memory vector index for semantic search
}

// SetLLM sets the LLM client for embedding generation and builds the vector index.
func (s *Store) SetLLM(client *llm.Client) {
	s.llm = client
	s.vecIndex = NewVecIndex()
	// Build index from existing embeddings in background
	go s.rebuildVecIndex()
}

// rebuildVecIndex loads all embeddings from the database and builds the cover tree.
func (s *Store) rebuildVecIndex() {
	rows, err := s.db.Query(
		`SELECT id, embedding FROM memories WHERE embedding != '' AND embedding IS NOT NULL`,
	)
	if err != nil {
		fmt.Printf("[memory] Warning: failed to load embeddings for index: %v\n", err)
		return
	}
	defer rows.Close()

	var ids []string
	var vecs [][]float32

	for rows.Next() {
		var id, embJSON string
		if err := rows.Scan(&id, &embJSON); err != nil {
			continue
		}
		vec, err := ParseEmbedding(embJSON)
		if err != nil || vec == nil {
			continue
		}
		ids = append(ids, id)
		vecs = append(vecs, vec)
	}

	if len(ids) == 0 {
		s.vecIndex.Build(nil, nil)
		fmt.Println("[memory] Vector index: empty (no embeddings yet)")
		return
	}

	if err := s.vecIndex.Build(ids, vecs); err != nil {
		fmt.Printf("[memory] Warning: failed to build vector index: %v\n", err)
		return
	}
	fmt.Printf("[memory] Vector index built: %d memories indexed\n", len(ids))
}

type Memory struct {
	ID             string                 `json:"id"`
	AgentID        string                 `json:"agent_id"`
	Namespace      string                 `json:"namespace"`
	Key            string                 `json:"key"`
	Content        string                 `json:"content"`
	ContentType    string                 `json:"content_type"`
	SourceType     string                 `json:"source_type"`
	SourceID       string                 `json:"source_id"`
	Importance     float64                `json:"importance"`
	AccessCount    int                    `json:"access_count"`
	Tags           []string               `json:"tags"`
	Metadata       map[string]interface{} `json:"metadata"`
	CreatedAt      string                 `json:"created_at"`
	ModifiedAt     string                 `json:"modified_at"`
	LastAccessedAt string                 `json:"last_accessed_at,omitempty"`
	ExpiresAt      string                 `json:"expires_at,omitempty"`
}

type ScratchEntry struct {
	ID        string `json:"id"`
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	TTL       int    `json:"ttl_seconds"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

type Compression struct {
	ID               string                 `json:"id"`
	AgentID          string                 `json:"agent_id"`
	SourceType       string                 `json:"source_type"`
	SourceID         string                 `json:"source_id"`
	OriginalTokens   int                    `json:"original_tokens"`
	CompressedTokens int                    `json:"compressed_tokens"`
	OriginalHash     string                 `json:"original_hash"`
	Summary          string                 `json:"summary"`
	KeptKeys         []string               `json:"kept_keys"`
	DroppedKeys      []string               `json:"dropped_keys"`
	CompressionRatio float64                `json:"compression_ratio"`
	Method           string                 `json:"method"`
	Metadata         map[string]interface{} `json:"metadata"`
	CreatedAt        string                 `json:"created_at"`
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "memory", migrations); err != nil {
		return nil, fmt.Errorf("migrating memory schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// --- Long-term Memory ---

// CreateMemory inserts a new memory or updates an existing one with the same
// (agent_id, namespace, key) triple. This prevents duplicate memories when
// an agent stores the same key repeatedly.
func (s *Store) CreateMemory(m *Memory) (*Memory, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Metadata == nil {
		m.Metadata = map[string]interface{}{}
	}
	if m.Namespace == "" {
		m.Namespace = "default"
	}

	tagsJSON, _ := json.Marshal(m.Tags)
	metaJSON, _ := json.Marshal(m.Metadata)

	// Generate embedding if LLM client is available
	embeddingJSON := ""
	if s.llm != nil {
		embText := m.Key + ": " + m.Content
		if vec, err := s.llm.Embed(embText); err == nil {
			if j, err := json.Marshal(vec); err == nil {
				embeddingJSON = string(j)
			}
		}
	}

	// Check for existing memory with same (agent_id, namespace, key)
	var existingID string
	err := s.db.QueryRow(
		"SELECT id FROM memories WHERE agent_id = ? AND namespace = ? AND key = ?",
		m.AgentID, m.Namespace, m.Key,
	).Scan(&existingID)

	if err == nil {
		// Update existing memory
		m.ID = existingID
		m.ModifiedAt = now
		_, err = s.db.Exec(
			`UPDATE memories SET content = ?, content_type = ?, importance = ?,
			 tags = ?, metadata = ?, modified_at = ?, expires_at = ?, embedding = ?
			 WHERE id = ?`,
			m.Content, m.ContentType, m.Importance,
			string(tagsJSON), string(metaJSON), now, m.ExpiresAt, embeddingJSON, existingID,
		)
		if err != nil {
			return nil, fmt.Errorf("updating memory: %w", err)
		}
		s.indexMemFTS(existingID, m.AgentID, m.Key, m.Content, string(tagsJSON))
		return s.GetMemory(existingID)
	}

	// Insert new memory
	m.ID = newID("mem_")
	m.CreatedAt = now
	m.ModifiedAt = now

	_, err = s.db.Exec(
		`INSERT INTO memories (id, agent_id, namespace, key, content, content_type, source_type, source_id, importance, tags, metadata, created_at, modified_at, expires_at, embedding)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.AgentID, m.Namespace, m.Key, m.Content, m.ContentType, m.SourceType, m.SourceID,
		m.Importance, string(tagsJSON), string(metaJSON), now, now, m.ExpiresAt, embeddingJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("creating memory: %w", err)
	}
	s.indexMemFTS(m.ID, m.AgentID, m.Key, m.Content, string(tagsJSON))

	// Rebuild vector index if embeddings are enabled
	if s.vecIndex != nil && embeddingJSON != "" {
		go s.rebuildVecIndex()
	}

	return m, nil
}

func (s *Store) GetMemory(id string) (*Memory, error) {
	m := &Memory{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id, agent_id, namespace, key, content, content_type, source_type, source_id,
		        importance, access_count, tags, metadata, created_at, modified_at,
		        COALESCE(last_accessed_at,''), COALESCE(expires_at,'')
		 FROM memories WHERE id = ?`, id,
	).Scan(&m.ID, &m.AgentID, &m.Namespace, &m.Key, &m.Content, &m.ContentType,
		&m.SourceType, &m.SourceID, &m.Importance, &m.AccessCount,
		&tagsJSON, &metaJSON, &m.CreatedAt, &m.ModifiedAt, &m.LastAccessedAt, &m.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(tagsJSON), &m.Tags)
	json.Unmarshal([]byte(metaJSON), &m.Metadata)

	// Update access count (fire and forget)
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE memories SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?", now, id)
	m.AccessCount++
	m.LastAccessedAt = now
	return m, nil
}

type MemoryListOpts struct {
	AgentID       string
	Namespace     string
	SourceType    string
	Query         string
	ImportanceMin float64
	Limit         int
}

func (s *Store) ListMemories(opts MemoryListOpts) ([]Memory, error) {
	if opts.Query != "" {
		// Try semantic search first, fall back to FTS
		if s.llm != nil {
			results, err := s.semanticSearch(opts.Query, opts.AgentID, opts.Limit)
			if err == nil && len(results) > 0 {
				return results, nil
			}
			// Fall through to FTS on error
		}
		return s.searchMemories(opts.Query, opts.AgentID, opts.Limit)
	}
	query := `SELECT id, agent_id, namespace, key, content, content_type, source_type, source_id,
	          importance, access_count, tags, metadata, created_at, modified_at,
	          COALESCE(last_accessed_at,''), COALESCE(expires_at,'')
	          FROM memories WHERE 1=1`
	var args []interface{}
	if opts.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, opts.AgentID)
	}
	if opts.Namespace != "" {
		query += " AND namespace = ?"
		args = append(args, opts.Namespace)
	}
	if opts.SourceType != "" {
		query += " AND source_type = ?"
		args = append(args, opts.SourceType)
	}
	if opts.ImportanceMin > 0 {
		query += " AND importance >= ?"
		args = append(args, opts.ImportanceMin)
	}
	query += " ORDER BY importance DESC, modified_at DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 50"
	}

	return s.scanMemories(query, args...)
}

// searchMemories uses FTS5 with a JOIN instead of N+1 queries.
// Filters by agent_id at the FTS level when provided.
func (s *Store) searchMemories(q, agentID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}

	// Sanitize FTS query — escape special characters
	q = sanitizeFTSQuery(q)
	if q == "" {
		return nil, nil
	}

	// Build FTS match expression with optional agent_id filter
	var ftsMatch string
	if agentID != "" {
		ftsMatch = fmt.Sprintf("agent_id:%s AND (%s)", agentID, q)
	} else {
		ftsMatch = q
	}

	query := `SELECT m.id, m.agent_id, m.namespace, m.key, m.content, m.content_type,
	                 m.source_type, m.source_id, m.importance, m.access_count,
	                 m.tags, m.metadata, m.created_at, m.modified_at,
	                 COALESCE(m.last_accessed_at,''), COALESCE(m.expires_at,'')
	          FROM memories_fts f
	          JOIN memories_fts_map fm ON fm.rowid = f.rowid
	          JOIN memories m ON m.id = fm.memory_id
	          WHERE memories_fts MATCH ?
	          ORDER BY rank
	          LIMIT ?`

	return s.scanMemories(query, ftsMatch, limit)
}

// semanticSearch uses the cover tree vector index for fast kNN search.
func (s *Store) semanticSearch(query, agentID string, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if s.vecIndex == nil || !s.vecIndex.IsBuilt() || s.vecIndex.Count() == 0 {
		return nil, nil
	}

	// Embed the query
	queryVec, err := s.llm.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("embedding query: %w", err)
	}
	queryF32 := Float64ToFloat32(queryVec)

	// Query the cover tree index — O(log n) instead of O(n)
	matchIDs, scores, err := s.vecIndex.Query(queryF32, limit*2) // fetch extra to filter by agent
	if err != nil {
		return nil, fmt.Errorf("vector index query: %w", err)
	}

	// Fetch full memory records for matches
	var result []Memory
	for i, id := range matchIDs {
		if scores[i] < 0.3 { // minimum similarity threshold
			continue
		}
		m, err := s.GetMemory(id)
		if err != nil || m == nil {
			continue
		}
		// Filter by agent if specified
		if agentID != "" && m.AgentID != agentID {
			continue
		}
		result = append(result, *m)
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

// sanitizeFTSQuery escapes special FTS5 characters and handles common query patterns.
func sanitizeFTSQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	// If query contains FTS operators, pass through as-is
	if strings.ContainsAny(q, "\"*") {
		return q
	}
	// Otherwise, quote each word for prefix matching
	words := strings.Fields(q)
	var parts []string
	for _, w := range words {
		// Remove characters that break FTS
		w = strings.Map(func(r rune) rune {
			if r == ':' || r == '(' || r == ')' || r == '{' || r == '}' || r == '^' {
				return -1
			}
			return r
		}, w)
		if w != "" {
			parts = append(parts, "\""+w+"\"")
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " OR ")
}

func (s *Store) UpdateMemory(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at = ?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "content", "key", "namespace", "content_type", "source_type", "source_id", "expires_at":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		case "importance":
			sets = append(sets, "importance = ?")
			args = append(args, v)
		case "tags":
			j, _ := json.Marshal(v)
			sets = append(sets, "tags = ?")
			args = append(args, string(j))
		case "metadata":
			j, _ := json.Marshal(v)
			sets = append(sets, "metadata = ?")
			args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE memories SET " + strings.Join(sets, ", ") + " WHERE id = ?"
	_, err := s.db.Exec(q, args...)
	if err != nil {
		return err
	}

	// Re-index FTS if content or key changed
	if _, ok := updates["content"]; ok {
		m, _ := s.GetMemory(id)
		if m != nil {
			tagsJSON, _ := json.Marshal(m.Tags)
			s.indexMemFTS(id, m.AgentID, m.Key, m.Content, string(tagsJSON))
		}
	}
	return nil
}

func (s *Store) DeleteMemory(id string) error {
	// Clean up FTS
	var rowID int64
	if s.db.QueryRow("SELECT rowid FROM memories_fts_map WHERE memory_id = ?", id).Scan(&rowID) == nil {
		s.db.Exec("DELETE FROM memories_fts WHERE rowid = ?", rowID)
		s.db.Exec("DELETE FROM memories_fts_map WHERE memory_id = ?", id)
	}
	_, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	if err == nil && s.vecIndex != nil {
		go s.rebuildVecIndex()
	}
	return err
}

func (s *Store) scanMemories(query string, args ...interface{}) ([]Memory, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Memory
	for rows.Next() {
		var m Memory
		var tagsJSON, metaJSON string
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Namespace, &m.Key, &m.Content, &m.ContentType,
			&m.SourceType, &m.SourceID, &m.Importance, &m.AccessCount,
			&tagsJSON, &metaJSON, &m.CreatedAt, &m.ModifiedAt, &m.LastAccessedAt, &m.ExpiresAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tagsJSON), &m.Tags)
		json.Unmarshal([]byte(metaJSON), &m.Metadata)
		result = append(result, m)
	}
	return result, rows.Err()
}

// indexMemFTS updates the FTS index for a memory, now including agent_id.
func (s *Store) indexMemFTS(id, agentID, key, content, tags string) {
	var rowID int64
	if s.db.QueryRow("SELECT rowid FROM memories_fts_map WHERE memory_id = ?", id).Scan(&rowID) == nil {
		s.db.Exec("DELETE FROM memories_fts WHERE rowid = ?", rowID)
		s.db.Exec("DELETE FROM memories_fts_map WHERE memory_id = ?", id)
	}
	if res, err := s.db.Exec("INSERT INTO memories_fts (agent_id, key, content, tags) VALUES (?,?,?,?)", agentID, key, content, tags); err == nil {
		r, _ := res.LastInsertId()
		s.db.Exec("INSERT INTO memories_fts_map (memory_id, rowid) VALUES (?,?)", id, r)
	}
}

// --- Scratchpad ---

func (s *Store) SetScratch(agentID, sessionID, key, value string, ttl int) (*ScratchEntry, error) {
	id := newID("scratch_")
	now := time.Now().UTC()
	if ttl <= 0 {
		ttl = 3600
	}
	expires := now.Add(time.Duration(ttl) * time.Second).Format(time.RFC3339)

	_, err := s.db.Exec(
		`INSERT INTO scratchpad (id, agent_id, session_id, key, value, ttl_seconds, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`,
		id, agentID, sessionID, key, value, ttl, now.Format(time.RFC3339), expires,
	)
	if err != nil {
		return nil, err
	}
	return &ScratchEntry{ID: id, AgentID: agentID, SessionID: sessionID, Key: key, Value: value, TTL: ttl, CreatedAt: now.Format(time.RFC3339), ExpiresAt: expires}, nil
}

func (s *Store) GetScratch(id string) (*ScratchEntry, error) {
	e := &ScratchEntry{}
	err := s.db.QueryRow(
		"SELECT id, agent_id, session_id, key, value, ttl_seconds, created_at, expires_at FROM scratchpad WHERE id = ? AND expires_at > ?",
		id, time.Now().UTC().Format(time.RFC3339),
	).Scan(&e.ID, &e.AgentID, &e.SessionID, &e.Key, &e.Value, &e.TTL, &e.CreatedAt, &e.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *Store) ListScratch(agentID, sessionID string) ([]ScratchEntry, error) {
	q := "SELECT id, agent_id, session_id, key, value, ttl_seconds, created_at, expires_at FROM scratchpad WHERE expires_at > ?"
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	if agentID != "" {
		q += " AND agent_id = ?"
		args = append(args, agentID)
	}
	if sessionID != "" {
		q += " AND session_id = ?"
		args = append(args, sessionID)
	}
	q += " ORDER BY created_at DESC LIMIT 100"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ScratchEntry
	for rows.Next() {
		var e ScratchEntry
		rows.Scan(&e.ID, &e.AgentID, &e.SessionID, &e.Key, &e.Value, &e.TTL, &e.CreatedAt, &e.ExpiresAt)
		result = append(result, e)
	}
	return result, nil
}

func (s *Store) DeleteScratch(id string) error {
	_, err := s.db.Exec("DELETE FROM scratchpad WHERE id = ?", id)
	return err
}

func (s *Store) CleanupScratch() (int64, error) {
	res, err := s.db.Exec("DELETE FROM scratchpad WHERE expires_at <= ?", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Compression ---

func (s *Store) CreateCompression(c *Compression) (*Compression, error) {
	c.ID = newID("comp_")
	c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if c.KeptKeys == nil {
		c.KeptKeys = []string{}
	}
	if c.DroppedKeys == nil {
		c.DroppedKeys = []string{}
	}
	if c.Metadata == nil {
		c.Metadata = map[string]interface{}{}
	}

	kept, _ := json.Marshal(c.KeptKeys)
	dropped, _ := json.Marshal(c.DroppedKeys)
	meta, _ := json.Marshal(c.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO compressions (id,agent_id,source_type,source_id,original_tokens,compressed_tokens,original_hash,summary,kept_keys,dropped_keys,compression_ratio,method,metadata,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.AgentID, c.SourceType, c.SourceID, c.OriginalTokens, c.CompressedTokens,
		c.OriginalHash, c.Summary, string(kept), string(dropped), c.CompressionRatio, c.Method, string(meta), c.CreatedAt,
	)
	return c, err
}

func (s *Store) ListCompressions(agentID, sourceType string, limit int) ([]Compression, error) {
	q := "SELECT id,agent_id,source_type,source_id,original_tokens,compressed_tokens,original_hash,summary,kept_keys,dropped_keys,compression_ratio,method,metadata,created_at FROM compressions WHERE 1=1"
	var args []interface{}
	if agentID != "" {
		q += " AND agent_id = ?"
		args = append(args, agentID)
	}
	if sourceType != "" {
		q += " AND source_type = ?"
		args = append(args, sourceType)
	}
	q += " ORDER BY created_at DESC"
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	} else {
		q += " LIMIT 50"
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Compression
	for rows.Next() {
		var c Compression
		var kept, dropped, meta string
		rows.Scan(&c.ID, &c.AgentID, &c.SourceType, &c.SourceID, &c.OriginalTokens, &c.CompressedTokens,
			&c.OriginalHash, &c.Summary, &kept, &dropped, &c.CompressionRatio, &c.Method, &meta, &c.CreatedAt)
		json.Unmarshal([]byte(kept), &c.KeptKeys)
		json.Unmarshal([]byte(dropped), &c.DroppedKeys)
		json.Unmarshal([]byte(meta), &c.Metadata)
		result = append(result, c)
	}
	return result, nil
}
