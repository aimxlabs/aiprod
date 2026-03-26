package tools

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
	`CREATE TABLE IF NOT EXISTS tool_registry (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		version     TEXT DEFAULT '1.0',
		description TEXT DEFAULT '',
		category    TEXT DEFAULT 'general',
		input_schema  TEXT DEFAULT '{}',
		output_schema TEXT DEFAULT '{}',
		endpoint    TEXT DEFAULT '',
		method      TEXT DEFAULT 'POST',
		auth_scope  TEXT DEFAULT '',
		enabled     INTEGER DEFAULT 1,
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_name_ver ON tool_registry(name, version);
	CREATE INDEX IF NOT EXISTS idx_tool_cat ON tool_registry(category);
	CREATE INDEX IF NOT EXISTS idx_tool_enabled ON tool_registry(enabled);

	CREATE TABLE IF NOT EXISTS tool_executions (
		id          TEXT PRIMARY KEY,
		tool_id     TEXT NOT NULL REFERENCES tool_registry(id),
		agent_id    TEXT NOT NULL,
		trace_id    TEXT DEFAULT '',
		input       TEXT DEFAULT '{}',
		output      TEXT DEFAULT '',
		error       TEXT DEFAULT '',
		status      TEXT DEFAULT 'success',
		dry_run     INTEGER DEFAULT 0,
		duration_ms INTEGER DEFAULT 0,
		created_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_texec_tool ON tool_executions(tool_id);
	CREATE INDEX IF NOT EXISTS idx_texec_agent ON tool_executions(agent_id);
	CREATE INDEX IF NOT EXISTS idx_texec_trace ON tool_executions(trace_id) WHERE trace_id != '';

	CREATE TABLE IF NOT EXISTS tool_simulations (
		id            TEXT PRIMARY KEY,
		tool_id       TEXT NOT NULL REFERENCES tool_registry(id),
		agent_id      TEXT NOT NULL,
		input         TEXT DEFAULT '{}',
		expected_output TEXT DEFAULT '',
		side_effects  TEXT DEFAULT '[]',
		risk_level    TEXT DEFAULT 'low',
		approved      INTEGER DEFAULT 0,
		notes         TEXT DEFAULT '',
		created_at    TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_tsim_tool ON tool_simulations(tool_id);`,
}

type Store struct{ db *sql.DB }

type Tool struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Description  string                 `json:"description"`
	Category     string                 `json:"category"`
	InputSchema  map[string]interface{} `json:"input_schema"`
	OutputSchema map[string]interface{} `json:"output_schema"`
	Endpoint     string                 `json:"endpoint"`
	Method       string                 `json:"method"`
	AuthScope    string                 `json:"auth_scope"`
	Enabled      bool                   `json:"enabled"`
	Tags         []string               `json:"tags"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    string                 `json:"created_at"`
	ModifiedAt   string                 `json:"modified_at"`
}

type ToolExecution struct {
	ID         string                 `json:"id"`
	ToolID     string                 `json:"tool_id"`
	AgentID    string                 `json:"agent_id"`
	TraceID    string                 `json:"trace_id,omitempty"`
	Input      map[string]interface{} `json:"input"`
	Output     string                 `json:"output,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Status     string                 `json:"status"`
	DryRun     bool                   `json:"dry_run"`
	DurationMs int64                  `json:"duration_ms"`
	CreatedAt  string                 `json:"created_at"`
}

type Simulation struct {
	ID             string                 `json:"id"`
	ToolID         string                 `json:"tool_id"`
	AgentID        string                 `json:"agent_id"`
	Input          map[string]interface{} `json:"input"`
	ExpectedOutput string                 `json:"expected_output"`
	SideEffects    []string               `json:"side_effects"`
	RiskLevel      string                 `json:"risk_level"`
	Approved       bool                   `json:"approved"`
	Notes          string                 `json:"notes"`
	CreatedAt      string                 `json:"created_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "tools", migrations); err != nil {
		return nil, fmt.Errorf("migrating tools schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Tool Registry ---

func (s *Store) RegisterTool(t *Tool) (*Tool, error) {
	t.ID = newID("tool_")
	now := time.Now().UTC().Format(time.RFC3339)
	t.CreatedAt = now
	t.ModifiedAt = now
	if t.Version == "" { t.Version = "1.0" }
	if t.Category == "" { t.Category = "general" }
	if t.Method == "" { t.Method = "POST" }
	if t.Tags == nil { t.Tags = []string{} }
	if t.InputSchema == nil { t.InputSchema = map[string]interface{}{} }
	if t.OutputSchema == nil { t.OutputSchema = map[string]interface{}{} }
	if t.Metadata == nil { t.Metadata = map[string]interface{}{} }

	inSchema, _ := json.Marshal(t.InputSchema)
	outSchema, _ := json.Marshal(t.OutputSchema)
	tagsJSON, _ := json.Marshal(t.Tags)
	metaJSON, _ := json.Marshal(t.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO tool_registry (id,name,version,description,category,input_schema,output_schema,endpoint,method,auth_scope,enabled,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.Version, t.Description, t.Category,
		string(inSchema), string(outSchema), t.Endpoint, t.Method, t.AuthScope,
		boolToInt(t.Enabled), string(tagsJSON), string(metaJSON), now, now,
	)
	return t, err
}

func (s *Store) GetTool(id string) (*Tool, error) {
	t := &Tool{}
	var inSchema, outSchema, tagsJSON, metaJSON string
	var enabled int
	err := s.db.QueryRow(
		`SELECT id,name,version,description,category,input_schema,output_schema,endpoint,method,auth_scope,enabled,tags,metadata,created_at,modified_at
		 FROM tool_registry WHERE id=?`, id,
	).Scan(&t.ID, &t.Name, &t.Version, &t.Description, &t.Category,
		&inSchema, &outSchema, &t.Endpoint, &t.Method, &t.AuthScope,
		&enabled, &tagsJSON, &metaJSON, &t.CreatedAt, &t.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	t.Enabled = enabled != 0
	json.Unmarshal([]byte(inSchema), &t.InputSchema)
	json.Unmarshal([]byte(outSchema), &t.OutputSchema)
	json.Unmarshal([]byte(tagsJSON), &t.Tags)
	json.Unmarshal([]byte(metaJSON), &t.Metadata)
	return t, nil
}

func (s *Store) GetToolByName(name, version string) (*Tool, error) {
	if version == "" { version = "1.0" }
	t := &Tool{}
	var inSchema, outSchema, tagsJSON, metaJSON string
	var enabled int
	err := s.db.QueryRow(
		`SELECT id,name,version,description,category,input_schema,output_schema,endpoint,method,auth_scope,enabled,tags,metadata,created_at,modified_at
		 FROM tool_registry WHERE name=? AND version=?`, name, version,
	).Scan(&t.ID, &t.Name, &t.Version, &t.Description, &t.Category,
		&inSchema, &outSchema, &t.Endpoint, &t.Method, &t.AuthScope,
		&enabled, &tagsJSON, &metaJSON, &t.CreatedAt, &t.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	t.Enabled = enabled != 0
	json.Unmarshal([]byte(inSchema), &t.InputSchema)
	json.Unmarshal([]byte(outSchema), &t.OutputSchema)
	json.Unmarshal([]byte(tagsJSON), &t.Tags)
	json.Unmarshal([]byte(metaJSON), &t.Metadata)
	return t, nil
}

type ToolListOpts struct {
	Category string
	Enabled  *bool
	Query    string
	Limit    int
}

func (s *Store) ListTools(opts ToolListOpts) ([]Tool, error) {
	q := `SELECT id,name,version,description,category,input_schema,output_schema,endpoint,method,auth_scope,enabled,tags,metadata,created_at,modified_at
	      FROM tool_registry WHERE 1=1`
	var args []interface{}
	if opts.Category != "" { q += " AND category=?"; args = append(args, opts.Category) }
	if opts.Enabled != nil {
		q += " AND enabled=?"; args = append(args, boolToInt(*opts.Enabled))
	}
	if opts.Query != "" { q += " AND (name LIKE ? OR description LIKE ?)"; args = append(args, "%"+opts.Query+"%", "%"+opts.Query+"%") }
	q += " ORDER BY category, name"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 100" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Tool
	for rows.Next() {
		var t Tool
		var inSchema, outSchema, tagsJSON, metaJSON string
		var enabled int
		rows.Scan(&t.ID, &t.Name, &t.Version, &t.Description, &t.Category,
			&inSchema, &outSchema, &t.Endpoint, &t.Method, &t.AuthScope,
			&enabled, &tagsJSON, &metaJSON, &t.CreatedAt, &t.ModifiedAt)
		t.Enabled = enabled != 0
		json.Unmarshal([]byte(inSchema), &t.InputSchema)
		json.Unmarshal([]byte(outSchema), &t.OutputSchema)
		json.Unmarshal([]byte(tagsJSON), &t.Tags)
		json.Unmarshal([]byte(metaJSON), &t.Metadata)
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *Store) UpdateTool(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at = ?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "version", "description", "category", "endpoint", "method", "auth_scope":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "enabled":
			b, _ := v.(bool); sets = append(sets, "enabled=?"); args = append(args, boolToInt(b))
		case "input_schema", "output_schema", "metadata":
			j, _ := json.Marshal(v); sets = append(sets, k+"=?"); args = append(args, string(j))
		case "tags":
			j, _ := json.Marshal(v); sets = append(sets, "tags=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE tool_registry SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) DeleteTool(id string) error {
	_, err := s.db.Exec("DELETE FROM tool_registry WHERE id=?", id)
	return err
}

// --- Tool Executions ---

func (s *Store) RecordExecution(ex *ToolExecution) (*ToolExecution, error) {
	ex.ID = newID("texec_")
	ex.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if ex.Input == nil { ex.Input = map[string]interface{}{} }
	if ex.Status == "" { ex.Status = "success" }

	inputJSON, _ := json.Marshal(ex.Input)
	_, err := s.db.Exec(
		`INSERT INTO tool_executions (id,tool_id,agent_id,trace_id,input,output,error,status,dry_run,duration_ms,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		ex.ID, ex.ToolID, ex.AgentID, ex.TraceID,
		string(inputJSON), ex.Output, ex.Error, ex.Status,
		boolToInt(ex.DryRun), ex.DurationMs, ex.CreatedAt,
	)
	return ex, err
}

func (s *Store) ListExecutions(toolID, agentID string, limit int) ([]ToolExecution, error) {
	if limit <= 0 { limit = 50 }
	q := `SELECT id,tool_id,agent_id,trace_id,input,output,error,status,dry_run,duration_ms,created_at
	      FROM tool_executions WHERE 1=1`
	var args []interface{}
	if toolID != "" { q += " AND tool_id=?"; args = append(args, toolID) }
	if agentID != "" { q += " AND agent_id=?"; args = append(args, agentID) }
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []ToolExecution
	for rows.Next() {
		var ex ToolExecution
		var inputJSON string
		var dryRun int
		rows.Scan(&ex.ID, &ex.ToolID, &ex.AgentID, &ex.TraceID,
			&inputJSON, &ex.Output, &ex.Error, &ex.Status,
			&dryRun, &ex.DurationMs, &ex.CreatedAt)
		ex.DryRun = dryRun != 0
		json.Unmarshal([]byte(inputJSON), &ex.Input)
		result = append(result, ex)
	}
	return result, rows.Err()
}

// --- Simulations ---

func (s *Store) CreateSimulation(sim *Simulation) (*Simulation, error) {
	sim.ID = newID("sim_")
	sim.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if sim.Input == nil { sim.Input = map[string]interface{}{} }
	if sim.SideEffects == nil { sim.SideEffects = []string{} }
	if sim.RiskLevel == "" { sim.RiskLevel = "low" }

	inputJSON, _ := json.Marshal(sim.Input)
	effectsJSON, _ := json.Marshal(sim.SideEffects)

	_, err := s.db.Exec(
		`INSERT INTO tool_simulations (id,tool_id,agent_id,input,expected_output,side_effects,risk_level,approved,notes,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sim.ID, sim.ToolID, sim.AgentID, string(inputJSON), sim.ExpectedOutput,
		string(effectsJSON), sim.RiskLevel, boolToInt(sim.Approved), sim.Notes, sim.CreatedAt,
	)
	return sim, err
}

func (s *Store) ApproveSimulation(id string) error {
	_, err := s.db.Exec("UPDATE tool_simulations SET approved=1 WHERE id=?", id)
	return err
}

func (s *Store) ListSimulations(toolID string, limit int) ([]Simulation, error) {
	if limit <= 0 { limit = 50 }
	rows, err := s.db.Query(
		`SELECT id,tool_id,agent_id,input,expected_output,side_effects,risk_level,approved,notes,created_at
		 FROM tool_simulations WHERE tool_id=? ORDER BY created_at DESC LIMIT ?`, toolID, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Simulation
	for rows.Next() {
		var sim Simulation
		var inputJSON, effectsJSON string
		var approved int
		rows.Scan(&sim.ID, &sim.ToolID, &sim.AgentID, &inputJSON, &sim.ExpectedOutput,
			&effectsJSON, &sim.RiskLevel, &approved, &sim.Notes, &sim.CreatedAt)
		sim.Approved = approved != 0
		json.Unmarshal([]byte(inputJSON), &sim.Input)
		json.Unmarshal([]byte(effectsJSON), &sim.SideEffects)
		result = append(result, sim)
	}
	return result, rows.Err()
}

func boolToInt(b bool) int {
	if b { return 1 }
	return 0
}
