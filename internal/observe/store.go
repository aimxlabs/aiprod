package observe

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
	`CREATE TABLE IF NOT EXISTS traces (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		session_id  TEXT DEFAULT '',
		name        TEXT NOT NULL,
		trace_type  TEXT DEFAULT 'task',
		status      TEXT DEFAULT 'running',
		input       TEXT DEFAULT '{}',
		output      TEXT DEFAULT '',
		error       TEXT DEFAULT '',
		parent_id   TEXT DEFAULT '',
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		token_count INTEGER DEFAULT 0,
		cost        REAL DEFAULT 0.0,
		started_at  TEXT NOT NULL,
		ended_at    TEXT DEFAULT '',
		duration_ms INTEGER DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_trace_agent ON traces(agent_id);
	CREATE INDEX IF NOT EXISTS idx_trace_session ON traces(session_id) WHERE session_id != '';
	CREATE INDEX IF NOT EXISTS idx_trace_parent ON traces(parent_id) WHERE parent_id != '';
	CREATE INDEX IF NOT EXISTS idx_trace_status ON traces(status);
	CREATE INDEX IF NOT EXISTS idx_trace_started ON traces(started_at DESC);

	CREATE TABLE IF NOT EXISTS trace_steps (
		id         TEXT PRIMARY KEY,
		trace_id   TEXT NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
		seq        INTEGER NOT NULL,
		step_type  TEXT NOT NULL,
		name       TEXT NOT NULL,
		input      TEXT DEFAULT '{}',
		output     TEXT DEFAULT '',
		error      TEXT DEFAULT '',
		status     TEXT DEFAULT 'ok',
		started_at TEXT NOT NULL,
		ended_at   TEXT DEFAULT '',
		duration_ms INTEGER DEFAULT 0,
		metadata   TEXT DEFAULT '{}'
	);
	CREATE INDEX IF NOT EXISTS idx_step_trace ON trace_steps(trace_id, seq);

	CREATE TABLE IF NOT EXISTS replay_snapshots (
		id         TEXT PRIMARY KEY,
		trace_id   TEXT NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
		step_seq   INTEGER NOT NULL,
		state      TEXT NOT NULL,
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_snap_trace ON replay_snapshots(trace_id, step_seq);

	CREATE TABLE IF NOT EXISTS failure_patterns (
		id             TEXT PRIMARY KEY,
		pattern_name   TEXT NOT NULL,
		description    TEXT DEFAULT '',
		error_regex    TEXT DEFAULT '',
		occurrences    INTEGER DEFAULT 1,
		last_trace_id  TEXT DEFAULT '',
		suggested_fix  TEXT DEFAULT '',
		auto_resolved  INTEGER DEFAULT 0,
		tags           TEXT DEFAULT '[]',
		metadata       TEXT DEFAULT '{}',
		created_at     TEXT NOT NULL,
		modified_at    TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_fp_name ON failure_patterns(pattern_name);`,
}

type Store struct{ db *sql.DB }

type Trace struct {
	ID        string                 `json:"id"`
	AgentID   string                 `json:"agent_id"`
	SessionID string                 `json:"session_id,omitempty"`
	Name      string                 `json:"name"`
	TraceType string                 `json:"trace_type"`
	Status    string                 `json:"status"`
	Input     map[string]interface{} `json:"input"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	ParentID  string                 `json:"parent_id,omitempty"`
	Tags      []string               `json:"tags"`
	Metadata  map[string]interface{} `json:"metadata"`
	TokenCount int                   `json:"token_count"`
	Cost      float64                `json:"cost"`
	StartedAt string                 `json:"started_at"`
	EndedAt   string                 `json:"ended_at,omitempty"`
	DurationMs int64                 `json:"duration_ms"`
}

type TraceStep struct {
	ID        string                 `json:"id"`
	TraceID   string                 `json:"trace_id"`
	Seq       int                    `json:"seq"`
	StepType  string                 `json:"step_type"`
	Name      string                 `json:"name"`
	Input     map[string]interface{} `json:"input"`
	Output    string                 `json:"output,omitempty"`
	Error     string                 `json:"error,omitempty"`
	Status    string                 `json:"status"`
	StartedAt string                 `json:"started_at"`
	EndedAt   string                 `json:"ended_at,omitempty"`
	DurationMs int64                 `json:"duration_ms"`
	Metadata  map[string]interface{} `json:"metadata"`
}

type ReplaySnapshot struct {
	ID        string `json:"id"`
	TraceID   string `json:"trace_id"`
	StepSeq   int    `json:"step_seq"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

type FailurePattern struct {
	ID            string                 `json:"id"`
	PatternName   string                 `json:"pattern_name"`
	Description   string                 `json:"description"`
	ErrorRegex    string                 `json:"error_regex"`
	Occurrences   int                    `json:"occurrences"`
	LastTraceID   string                 `json:"last_trace_id,omitempty"`
	SuggestedFix  string                 `json:"suggested_fix,omitempty"`
	AutoResolved  bool                   `json:"auto_resolved"`
	Tags          []string               `json:"tags"`
	Metadata      map[string]interface{} `json:"metadata"`
	CreatedAt     string                 `json:"created_at"`
	ModifiedAt    string                 `json:"modified_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(observeDB *sql.DB) (*Store, error) {
	if err := db.Migrate(observeDB, "observe", migrations); err != nil {
		return nil, fmt.Errorf("migrating observe schema: %w", err)
	}
	return &Store{db: observeDB}, nil
}

// --- Traces ---

func (s *Store) StartTrace(t *Trace) (*Trace, error) {
	t.ID = newID("trace_")
	t.StartedAt = time.Now().UTC().Format(time.RFC3339)
	t.Status = "running"
	if t.Tags == nil { t.Tags = []string{} }
	if t.Input == nil { t.Input = map[string]interface{}{} }
	if t.Metadata == nil { t.Metadata = map[string]interface{}{} }
	if t.TraceType == "" { t.TraceType = "task" }

	inputJSON, _ := json.Marshal(t.Input)
	tagsJSON, _ := json.Marshal(t.Tags)
	metaJSON, _ := json.Marshal(t.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO traces (id,agent_id,session_id,name,trace_type,status,input,parent_id,tags,metadata,started_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.AgentID, t.SessionID, t.Name, t.TraceType, t.Status,
		string(inputJSON), t.ParentID, string(tagsJSON), string(metaJSON), t.StartedAt,
	)
	return t, err
}

func (s *Store) EndTrace(id, status, output, errMsg string, tokenCount int, cost float64) error {
	now := time.Now().UTC()
	var startedAt string
	s.db.QueryRow("SELECT started_at FROM traces WHERE id = ?", id).Scan(&startedAt)
	var durationMs int64
	if st, err := time.Parse(time.RFC3339, startedAt); err == nil {
		durationMs = now.Sub(st).Milliseconds()
	}
	_, err := s.db.Exec(
		`UPDATE traces SET status=?, output=?, error=?, token_count=?, cost=?, ended_at=?, duration_ms=? WHERE id=?`,
		status, output, errMsg, tokenCount, cost, now.Format(time.RFC3339), durationMs, id,
	)
	return err
}

func (s *Store) GetTrace(id string) (*Trace, error) {
	t := &Trace{}
	var inputJSON, tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,agent_id,session_id,name,trace_type,status,input,output,error,parent_id,
		        tags,metadata,token_count,cost,started_at,COALESCE(ended_at,''),duration_ms
		 FROM traces WHERE id=?`, id,
	).Scan(&t.ID, &t.AgentID, &t.SessionID, &t.Name, &t.TraceType, &t.Status,
		&inputJSON, &t.Output, &t.Error, &t.ParentID,
		&tagsJSON, &metaJSON, &t.TokenCount, &t.Cost, &t.StartedAt, &t.EndedAt, &t.DurationMs)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(inputJSON), &t.Input)
	json.Unmarshal([]byte(tagsJSON), &t.Tags)
	json.Unmarshal([]byte(metaJSON), &t.Metadata)
	return t, nil
}

type TraceListOpts struct {
	AgentID   string
	SessionID string
	Status    string
	TraceType string
	ParentID  string
	Limit     int
}

func (s *Store) ListTraces(opts TraceListOpts) ([]Trace, error) {
	q := `SELECT id,agent_id,session_id,name,trace_type,status,input,output,error,parent_id,
	      tags,metadata,token_count,cost,started_at,COALESCE(ended_at,''),duration_ms
	      FROM traces WHERE 1=1`
	var args []interface{}
	if opts.AgentID != "" { q += " AND agent_id=?"; args = append(args, opts.AgentID) }
	if opts.SessionID != "" { q += " AND session_id=?"; args = append(args, opts.SessionID) }
	if opts.Status != "" { q += " AND status=?"; args = append(args, opts.Status) }
	if opts.TraceType != "" { q += " AND trace_type=?"; args = append(args, opts.TraceType) }
	if opts.ParentID != "" { q += " AND parent_id=?"; args = append(args, opts.ParentID) }
	q += " ORDER BY started_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Trace
	for rows.Next() {
		var t Trace
		var inputJSON, tagsJSON, metaJSON string
		rows.Scan(&t.ID, &t.AgentID, &t.SessionID, &t.Name, &t.TraceType, &t.Status,
			&inputJSON, &t.Output, &t.Error, &t.ParentID,
			&tagsJSON, &metaJSON, &t.TokenCount, &t.Cost, &t.StartedAt, &t.EndedAt, &t.DurationMs)
		json.Unmarshal([]byte(inputJSON), &t.Input)
		json.Unmarshal([]byte(tagsJSON), &t.Tags)
		json.Unmarshal([]byte(metaJSON), &t.Metadata)
		result = append(result, t)
	}
	return result, rows.Err()
}

// --- Trace Steps ---

func (s *Store) AddStep(step *TraceStep) (*TraceStep, error) {
	step.ID = newID("step_")
	step.StartedAt = time.Now().UTC().Format(time.RFC3339)
	if step.Input == nil { step.Input = map[string]interface{}{} }
	if step.Metadata == nil { step.Metadata = map[string]interface{}{} }
	if step.Status == "" { step.Status = "ok" }

	// Auto-assign seq
	if step.Seq == 0 {
		s.db.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM trace_steps WHERE trace_id=?", step.TraceID).Scan(&step.Seq)
	}

	inputJSON, _ := json.Marshal(step.Input)
	metaJSON, _ := json.Marshal(step.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO trace_steps (id,trace_id,seq,step_type,name,input,output,error,status,started_at,ended_at,duration_ms,metadata)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		step.ID, step.TraceID, step.Seq, step.StepType, step.Name,
		string(inputJSON), step.Output, step.Error, step.Status,
		step.StartedAt, step.EndedAt, step.DurationMs, string(metaJSON),
	)
	return step, err
}

func (s *Store) GetSteps(traceID string) ([]TraceStep, error) {
	rows, err := s.db.Query(
		`SELECT id,trace_id,seq,step_type,name,input,output,error,status,started_at,COALESCE(ended_at,''),duration_ms,metadata
		 FROM trace_steps WHERE trace_id=? ORDER BY seq`, traceID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []TraceStep
	for rows.Next() {
		var st TraceStep
		var inputJSON, metaJSON string
		rows.Scan(&st.ID, &st.TraceID, &st.Seq, &st.StepType, &st.Name,
			&inputJSON, &st.Output, &st.Error, &st.Status,
			&st.StartedAt, &st.EndedAt, &st.DurationMs, &metaJSON)
		json.Unmarshal([]byte(inputJSON), &st.Input)
		json.Unmarshal([]byte(metaJSON), &st.Metadata)
		result = append(result, st)
	}
	return result, rows.Err()
}

// --- Replay Snapshots ---

func (s *Store) SaveSnapshot(traceID string, stepSeq int, state string) (*ReplaySnapshot, error) {
	snap := &ReplaySnapshot{
		ID:        newID("snap_"),
		TraceID:   traceID,
		StepSeq:   stepSeq,
		State:     state,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	_, err := s.db.Exec(
		"INSERT INTO replay_snapshots (id,trace_id,step_seq,state,created_at) VALUES (?,?,?,?,?)",
		snap.ID, snap.TraceID, snap.StepSeq, snap.State, snap.CreatedAt,
	)
	return snap, err
}

func (s *Store) GetSnapshots(traceID string) ([]ReplaySnapshot, error) {
	rows, err := s.db.Query(
		"SELECT id,trace_id,step_seq,state,created_at FROM replay_snapshots WHERE trace_id=? ORDER BY step_seq", traceID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []ReplaySnapshot
	for rows.Next() {
		var sn ReplaySnapshot
		rows.Scan(&sn.ID, &sn.TraceID, &sn.StepSeq, &sn.State, &sn.CreatedAt)
		result = append(result, sn)
	}
	return result, rows.Err()
}

// --- Failure Patterns ---

func (s *Store) RecordFailure(fp *FailurePattern) (*FailurePattern, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Check if pattern already exists
	var existingID string
	err := s.db.QueryRow("SELECT id FROM failure_patterns WHERE pattern_name=?", fp.PatternName).Scan(&existingID)
	if err == nil {
		// Increment occurrences
		s.db.Exec("UPDATE failure_patterns SET occurrences=occurrences+1, last_trace_id=?, modified_at=? WHERE id=?",
			fp.LastTraceID, now, existingID)
		fp.ID = existingID
		return fp, nil
	}

	fp.ID = newID("fp_")
	fp.CreatedAt = now
	fp.ModifiedAt = now
	if fp.Tags == nil { fp.Tags = []string{} }
	if fp.Metadata == nil { fp.Metadata = map[string]interface{}{} }

	tagsJSON, _ := json.Marshal(fp.Tags)
	metaJSON, _ := json.Marshal(fp.Metadata)

	_, err = s.db.Exec(
		`INSERT INTO failure_patterns (id,pattern_name,description,error_regex,occurrences,last_trace_id,suggested_fix,auto_resolved,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		fp.ID, fp.PatternName, fp.Description, fp.ErrorRegex, fp.Occurrences,
		fp.LastTraceID, fp.SuggestedFix, boolToInt(fp.AutoResolved),
		string(tagsJSON), string(metaJSON), fp.CreatedAt, fp.ModifiedAt,
	)
	return fp, err
}

func (s *Store) ListFailurePatterns(limit int) ([]FailurePattern, error) {
	if limit <= 0 { limit = 50 }
	rows, err := s.db.Query(
		`SELECT id,pattern_name,description,error_regex,occurrences,last_trace_id,suggested_fix,auto_resolved,tags,metadata,created_at,modified_at
		 FROM failure_patterns ORDER BY occurrences DESC LIMIT ?`, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []FailurePattern
	for rows.Next() {
		var fp FailurePattern
		var resolved int
		var tagsJSON, metaJSON string
		rows.Scan(&fp.ID, &fp.PatternName, &fp.Description, &fp.ErrorRegex, &fp.Occurrences,
			&fp.LastTraceID, &fp.SuggestedFix, &resolved, &tagsJSON, &metaJSON, &fp.CreatedAt, &fp.ModifiedAt)
		fp.AutoResolved = resolved != 0
		json.Unmarshal([]byte(tagsJSON), &fp.Tags)
		json.Unmarshal([]byte(metaJSON), &fp.Metadata)
		result = append(result, fp)
	}
	return result, rows.Err()
}

// --- Stats ---

func (s *Store) AgentStats(agentID string) (map[string]interface{}, error) {
	var totalTraces, failedTraces, totalTokens int64
	var totalCost, avgDuration float64
	s.db.QueryRow("SELECT COUNT(*) FROM traces WHERE agent_id=?", agentID).Scan(&totalTraces)
	s.db.QueryRow("SELECT COUNT(*) FROM traces WHERE agent_id=? AND status='failed'", agentID).Scan(&failedTraces)
	s.db.QueryRow("SELECT COALESCE(SUM(token_count),0) FROM traces WHERE agent_id=?", agentID).Scan(&totalTokens)
	s.db.QueryRow("SELECT COALESCE(SUM(cost),0) FROM traces WHERE agent_id=?", agentID).Scan(&totalCost)
	s.db.QueryRow("SELECT COALESCE(AVG(duration_ms),0) FROM traces WHERE agent_id=? AND duration_ms>0", agentID).Scan(&avgDuration)
	return map[string]interface{}{
		"agent_id":       agentID,
		"total_traces":   totalTraces,
		"failed_traces":  failedTraces,
		"total_tokens":   totalTokens,
		"total_cost":     totalCost,
		"avg_duration_ms": avgDuration,
	}, nil
}

func boolToInt(b bool) int {
	if b { return 1 }
	return 0
}
