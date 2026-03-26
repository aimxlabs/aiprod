package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/garett/aiprod/internal/db"
)

var coreMigrations = []string{
	`CREATE TABLE IF NOT EXISTS agents (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		created_at  TEXT NOT NULL,
		active      INTEGER DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS api_keys (
		id         TEXT PRIMARY KEY,
		key_hash   TEXT NOT NULL UNIQUE,
		agent_id   TEXT NOT NULL REFERENCES agents(id),
		name       TEXT DEFAULT '',
		scopes     TEXT NOT NULL DEFAULT '[]',
		expires_at TEXT,
		created_at TEXT NOT NULL,
		last_used  TEXT,
		active     INTEGER DEFAULT 1
	);

	CREATE TABLE IF NOT EXISTS audit_log (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT,
		action      TEXT NOT NULL,
		resource_id TEXT,
		details     TEXT,
		ip_address  TEXT,
		created_at  TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_audit_agent ON audit_log(agent_id);
	CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
	CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_log(created_at);`,
}

type Store struct {
	db *sql.DB
}

func NewStore(dbPath string) (*Store, error) {
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(database, "auth", coreMigrations); err != nil {
		return nil, fmt.Errorf("migrating auth schema: %w", err)
	}
	return &Store{db: database}, nil
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	return s.db.Close()
}

type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	Active      bool   `json:"active"`
}

type APIKey struct {
	ID        string   `json:"id"`
	AgentID   string   `json:"agent_id"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	ExpiresAt string   `json:"expires_at,omitempty"`
	CreatedAt string   `json:"created_at"`
	LastUsed  string   `json:"last_used,omitempty"`
	Active    bool     `json:"active"`
}

type AuditEntry struct {
	ID         string `json:"id"`
	AgentID    string `json:"agent_id"`
	Action     string `json:"action"`
	ResourceID string `json:"resource_id,omitempty"`
	Details    string `json:"details,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
	CreatedAt  string `json:"created_at"`
}

func (s *Store) CreateAgent(name, description string) (*Agent, error) {
	id := "agent:" + name
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		"INSERT INTO agents (id, name, description, created_at) VALUES (?, ?, ?, ?)",
		id, name, description, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating agent: %w", err)
	}
	return &Agent{ID: id, Name: name, Description: description, CreatedAt: now, Active: true}, nil
}

func (s *Store) GetAgent(id string) (*Agent, error) {
	a := &Agent{}
	err := s.db.QueryRow(
		"SELECT id, name, description, created_at, active FROM agents WHERE id = ?", id,
	).Scan(&a.ID, &a.Name, &a.Description, &a.CreatedAt, &a.Active)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting agent: %w", err)
	}
	return a, nil
}

func (s *Store) ListAgents() ([]Agent, error) {
	rows, err := s.db.Query("SELECT id, name, description, created_at, active FROM agents ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.CreatedAt, &a.Active); err != nil {
			return nil, fmt.Errorf("scanning agent: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

func generateRawKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "aiprod_live_" + hex.EncodeToString(b), nil
}

func hashKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func generateID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateAPIKey returns the key object and the raw key string (shown only once).
func (s *Store) CreateAPIKey(agentID, name string, scopes []string, expiresAt string) (*APIKey, string, error) {
	rawKey, err := generateRawKey()
	if err != nil {
		return nil, "", fmt.Errorf("generating key: %w", err)
	}

	id := "key_" + generateID()
	now := time.Now().UTC().Format(time.RFC3339)
	scopesJSON, _ := json.Marshal(scopes)

	var expPtr *string
	if expiresAt != "" {
		expPtr = &expiresAt
	}

	_, err = s.db.Exec(
		"INSERT INTO api_keys (id, key_hash, agent_id, name, scopes, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, hashKey(rawKey), agentID, name, string(scopesJSON), expPtr, now,
	)
	if err != nil {
		return nil, "", fmt.Errorf("creating api key: %w", err)
	}

	key := &APIKey{
		ID:        id,
		AgentID:   agentID,
		Name:      name,
		Scopes:    scopes,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		Active:    true,
	}
	return key, rawKey, nil
}

// ValidateKey checks a raw API key and returns the associated agent ID and scopes.
func (s *Store) ValidateKey(rawKey string) (agentID string, scopes []string, err error) {
	h := hashKey(rawKey)
	var scopesJSON string
	var expiresAt sql.NullString
	err = s.db.QueryRow(
		"SELECT agent_id, scopes, expires_at FROM api_keys WHERE key_hash = ? AND active = 1", h,
	).Scan(&agentID, &scopesJSON, &expiresAt)
	if err == sql.ErrNoRows {
		return "", nil, fmt.Errorf("invalid or inactive key")
	}
	if err != nil {
		return "", nil, fmt.Errorf("validating key: %w", err)
	}

	if expiresAt.Valid {
		exp, err := time.Parse(time.RFC3339, expiresAt.String)
		if err == nil && time.Now().After(exp) {
			return "", nil, fmt.Errorf("key expired")
		}
	}

	json.Unmarshal([]byte(scopesJSON), &scopes)

	// Update last_used
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE api_keys SET last_used = ? WHERE key_hash = ?", now, h)

	return agentID, scopes, nil
}

func (s *Store) RevokeKey(keyID string) error {
	_, err := s.db.Exec("UPDATE api_keys SET active = 0 WHERE id = ?", keyID)
	return err
}

func (s *Store) ListKeys(agentID string) ([]APIKey, error) {
	query := "SELECT id, agent_id, name, scopes, COALESCE(expires_at,''), created_at, COALESCE(last_used,''), active FROM api_keys"
	var args []interface{}
	if agentID != "" {
		query += " WHERE agent_id = ?"
		args = append(args, agentID)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		var scopesJSON string
		if err := rows.Scan(&k.ID, &k.AgentID, &k.Name, &scopesJSON, &k.ExpiresAt, &k.CreatedAt, &k.LastUsed, &k.Active); err != nil {
			return nil, fmt.Errorf("scanning key: %w", err)
		}
		json.Unmarshal([]byte(scopesJSON), &k.Scopes)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// CheckScope returns true if the given scopes grant access to the required scope.
func CheckScope(granted []string, required string) bool {
	for _, s := range granted {
		if s == "*" {
			return true
		}
		if s == required {
			return true
		}
		// Wildcard: "docs:*" matches "docs:read", "docs:write", etc.
		if strings.HasSuffix(s, ":*") {
			prefix := strings.TrimSuffix(s, "*")
			if strings.HasPrefix(required, prefix) {
				return true
			}
		}
	}
	return false
}

func (s *Store) LogAudit(agentID, action, resourceID, details, ipAddress string) error {
	id := generateID()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		"INSERT INTO audit_log (id, agent_id, action, resource_id, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		id, agentID, action, resourceID, details, ipAddress, now,
	)
	return err
}

func (s *Store) QueryAudit(agentID, action, since string, limit int) ([]AuditEntry, error) {
	query := "SELECT id, COALESCE(agent_id,''), action, COALESCE(resource_id,''), COALESCE(details,''), COALESCE(ip_address,''), created_at FROM audit_log WHERE 1=1"
	var args []interface{}
	if agentID != "" {
		query += " AND agent_id = ?"
		args = append(args, agentID)
	}
	if action != "" {
		query += " AND action = ?"
		args = append(args, action)
	}
	if since != "" {
		query += " AND created_at >= ?"
		args = append(args, since)
	}
	query += " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit: %w", err)
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Action, &e.ResourceID, &e.Details, &e.IPAddress, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning audit: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
