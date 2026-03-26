package governor

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
	`CREATE TABLE IF NOT EXISTS budgets (
		id            TEXT PRIMARY KEY,
		agent_id      TEXT NOT NULL,
		resource_type TEXT NOT NULL,
		budget_limit  REAL NOT NULL,
		used          REAL DEFAULT 0.0,
		period        TEXT DEFAULT 'daily',
		period_start  TEXT NOT NULL,
		period_end    TEXT NOT NULL,
		auto_reset    INTEGER DEFAULT 1,
		alert_at      REAL DEFAULT 0.8,
		metadata      TEXT DEFAULT '{}',
		created_at    TEXT NOT NULL,
		modified_at   TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_budget_agent_res ON budgets(agent_id, resource_type, period_start);
	CREATE INDEX IF NOT EXISTS idx_budget_period ON budgets(period_end);

	CREATE TABLE IF NOT EXISTS budget_events (
		id          TEXT PRIMARY KEY,
		budget_id   TEXT NOT NULL REFERENCES budgets(id) ON DELETE CASCADE,
		agent_id    TEXT NOT NULL,
		amount      REAL NOT NULL,
		balance     REAL NOT NULL,
		event_type  TEXT DEFAULT 'usage',
		description TEXT DEFAULT '',
		created_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_bevt_budget ON budget_events(budget_id);
	CREATE INDEX IF NOT EXISTS idx_bevt_agent ON budget_events(agent_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS prompt_versions (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		version     INTEGER NOT NULL,
		content     TEXT NOT NULL,
		variables   TEXT DEFAULT '[]',
		model       TEXT DEFAULT '',
		temperature REAL DEFAULT 0.0,
		max_tokens  INTEGER DEFAULT 0,
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		active      INTEGER DEFAULT 1,
		created_by  TEXT DEFAULT '',
		created_at  TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_name_ver ON prompt_versions(name, version);
	CREATE INDEX IF NOT EXISTS idx_prompt_active ON prompt_versions(name, active);

	CREATE TABLE IF NOT EXISTS strategies (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		strategy_type TEXT DEFAULT 'general',
		rules       TEXT DEFAULT '[]',
		priority    INTEGER DEFAULT 0,
		enabled     INTEGER DEFAULT 1,
		agent_id    TEXT DEFAULT '',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_strategy_name ON strategies(name);
	CREATE INDEX IF NOT EXISTS idx_strategy_type ON strategies(strategy_type);`,
}

type Store struct{ db *sql.DB }

type Budget struct {
	ID           string                 `json:"id"`
	AgentID      string                 `json:"agent_id"`
	ResourceType string                 `json:"resource_type"`
	Limit        float64                `json:"budget_limit"`
	Used         float64                `json:"used"`
	Period       string                 `json:"period"`
	PeriodStart  string                 `json:"period_start"`
	PeriodEnd    string                 `json:"period_end"`
	AutoReset    bool                   `json:"auto_reset"`
	AlertAt      float64                `json:"alert_at"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    string                 `json:"created_at"`
	ModifiedAt   string                 `json:"modified_at"`
}

type BudgetEvent struct {
	ID          string  `json:"id"`
	BudgetID    string  `json:"budget_id"`
	AgentID     string  `json:"agent_id"`
	Amount      float64 `json:"amount"`
	Balance     float64 `json:"balance"`
	EventType   string  `json:"event_type"`
	Description string  `json:"description"`
	CreatedAt   string  `json:"created_at"`
}

type PromptVersion struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Version     int                    `json:"version"`
	Content     string                 `json:"content"`
	Variables   []string               `json:"variables"`
	Model       string                 `json:"model,omitempty"`
	Temperature float64                `json:"temperature"`
	MaxTokens   int                    `json:"max_tokens,omitempty"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	Active      bool                   `json:"active"`
	CreatedBy   string                 `json:"created_by,omitempty"`
	CreatedAt   string                 `json:"created_at"`
}

type Strategy struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	StrategyType string                 `json:"strategy_type"`
	Rules        []interface{}          `json:"rules"`
	Priority     int                    `json:"priority"`
	Enabled      bool                   `json:"enabled"`
	AgentID      string                 `json:"agent_id,omitempty"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    string                 `json:"created_at"`
	ModifiedAt   string                 `json:"modified_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "governor", migrations); err != nil {
		return nil, fmt.Errorf("migrating governor schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Budgets ---

func (s *Store) CreateBudget(b *Budget) (*Budget, error) {
	b.ID = newID("budget_")
	now := time.Now().UTC().Format(time.RFC3339)
	b.CreatedAt = now
	b.ModifiedAt = now
	if b.Period == "" { b.Period = "daily" }
	if b.AlertAt == 0 { b.AlertAt = 0.8 }
	if b.Metadata == nil { b.Metadata = map[string]interface{}{} }

	metaJSON, _ := json.Marshal(b.Metadata)
	_, err := s.db.Exec(
		`INSERT INTO budgets (id,agent_id,resource_type,budget_limit,used,period,period_start,period_end,auto_reset,alert_at,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.AgentID, b.ResourceType, b.Limit, b.Used, b.Period,
		b.PeriodStart, b.PeriodEnd, boolToInt(b.AutoReset), b.AlertAt,
		string(metaJSON), now, now,
	)
	return b, err
}

func (s *Store) GetBudget(id string) (*Budget, error) {
	b := &Budget{}
	var metaJSON string
	var autoReset int
	err := s.db.QueryRow(
		`SELECT id,agent_id,resource_type,budget_limit,used,period,period_start,period_end,auto_reset,alert_at,metadata,created_at,modified_at
		 FROM budgets WHERE id=?`, id,
	).Scan(&b.ID, &b.AgentID, &b.ResourceType, &b.Limit, &b.Used, &b.Period,
		&b.PeriodStart, &b.PeriodEnd, &autoReset, &b.AlertAt, &metaJSON, &b.CreatedAt, &b.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	b.AutoReset = autoReset != 0
	json.Unmarshal([]byte(metaJSON), &b.Metadata)
	return b, nil
}

func (s *Store) GetAgentBudget(agentID, resourceType string) (*Budget, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	b := &Budget{}
	var metaJSON string
	var autoReset int
	err := s.db.QueryRow(
		`SELECT id,agent_id,resource_type,budget_limit,used,period,period_start,period_end,auto_reset,alert_at,metadata,created_at,modified_at
		 FROM budgets WHERE agent_id=? AND resource_type=? AND period_end>=? ORDER BY period_start DESC LIMIT 1`,
		agentID, resourceType, now,
	).Scan(&b.ID, &b.AgentID, &b.ResourceType, &b.Limit, &b.Used, &b.Period,
		&b.PeriodStart, &b.PeriodEnd, &autoReset, &b.AlertAt, &metaJSON, &b.CreatedAt, &b.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	b.AutoReset = autoReset != 0
	json.Unmarshal([]byte(metaJSON), &b.Metadata)
	return b, nil
}

func (s *Store) ListBudgets(agentID string) ([]Budget, error) {
	q := `SELECT id,agent_id,resource_type,budget_limit,used,period,period_start,period_end,auto_reset,alert_at,metadata,created_at,modified_at
	      FROM budgets WHERE 1=1`
	var args []interface{}
	if agentID != "" { q += " AND agent_id=?"; args = append(args, agentID) }
	q += " ORDER BY period_end DESC LIMIT 100"

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Budget
	for rows.Next() {
		var b Budget
		var metaJSON string
		var autoReset int
		rows.Scan(&b.ID, &b.AgentID, &b.ResourceType, &b.Limit, &b.Used, &b.Period,
			&b.PeriodStart, &b.PeriodEnd, &autoReset, &b.AlertAt, &metaJSON, &b.CreatedAt, &b.ModifiedAt)
		b.AutoReset = autoReset != 0
		json.Unmarshal([]byte(metaJSON), &b.Metadata)
		result = append(result, b)
	}
	return result, rows.Err()
}

// Spend records usage against a budget. Returns remaining balance and whether alert threshold was crossed.
func (s *Store) Spend(agentID, resourceType string, amount float64, desc string) (remaining float64, alert bool, err error) {
	budget, err := s.GetAgentBudget(agentID, resourceType)
	if err != nil { return 0, false, err }
	if budget == nil { return 0, false, fmt.Errorf("no active budget for agent=%s resource=%s", agentID, resourceType) }

	newUsed := budget.Used + amount
	if newUsed > budget.Limit {
		return budget.Limit - budget.Used, false, fmt.Errorf("budget exceeded: used=%.2f + amount=%.2f > limit=%.2f", budget.Used, amount, budget.Limit)
	}

	remaining = budget.Limit - newUsed
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE budgets SET used=?, modified_at=? WHERE id=?", newUsed, now, budget.ID)

	// Record event
	evt := newID("bevt_")
	s.db.Exec(
		"INSERT INTO budget_events (id,budget_id,agent_id,amount,balance,event_type,description,created_at) VALUES (?,?,?,?,?,?,?,?)",
		evt, budget.ID, agentID, amount, remaining, "usage", desc, now,
	)

	alert = (newUsed / budget.Limit) >= budget.AlertAt
	return remaining, alert, nil
}

func (s *Store) GetBudgetEvents(budgetID string, limit int) ([]BudgetEvent, error) {
	if limit <= 0 { limit = 50 }
	rows, err := s.db.Query(
		"SELECT id,budget_id,agent_id,amount,balance,event_type,description,created_at FROM budget_events WHERE budget_id=? ORDER BY created_at DESC LIMIT ?",
		budgetID, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []BudgetEvent
	for rows.Next() {
		var e BudgetEvent
		rows.Scan(&e.ID, &e.BudgetID, &e.AgentID, &e.Amount, &e.Balance, &e.EventType, &e.Description, &e.CreatedAt)
		result = append(result, e)
	}
	return result, rows.Err()
}

// --- Prompt Versions ---

func (s *Store) CreatePrompt(p *PromptVersion) (*PromptVersion, error) {
	p.ID = newID("prompt_")
	p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if p.Variables == nil { p.Variables = []string{} }
	if p.Tags == nil { p.Tags = []string{} }
	if p.Metadata == nil { p.Metadata = map[string]interface{}{} }

	// Auto-version: find max version for this name
	if p.Version == 0 {
		s.db.QueryRow("SELECT COALESCE(MAX(version),0)+1 FROM prompt_versions WHERE name=?", p.Name).Scan(&p.Version)
	}

	varsJSON, _ := json.Marshal(p.Variables)
	tagsJSON, _ := json.Marshal(p.Tags)
	metaJSON, _ := json.Marshal(p.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO prompt_versions (id,name,version,content,variables,model,temperature,max_tokens,tags,metadata,active,created_by,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Version, p.Content, string(varsJSON), p.Model,
		p.Temperature, p.MaxTokens, string(tagsJSON), string(metaJSON),
		boolToInt(p.Active), p.CreatedBy, p.CreatedAt,
	)
	return p, err
}

func (s *Store) GetActivePrompt(name string) (*PromptVersion, error) {
	p := &PromptVersion{}
	var varsJSON, tagsJSON, metaJSON string
	var active int
	err := s.db.QueryRow(
		`SELECT id,name,version,content,variables,model,temperature,max_tokens,tags,metadata,active,created_by,created_at
		 FROM prompt_versions WHERE name=? AND active=1 ORDER BY version DESC LIMIT 1`, name,
	).Scan(&p.ID, &p.Name, &p.Version, &p.Content, &varsJSON, &p.Model,
		&p.Temperature, &p.MaxTokens, &tagsJSON, &metaJSON, &active, &p.CreatedBy, &p.CreatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	p.Active = active != 0
	json.Unmarshal([]byte(varsJSON), &p.Variables)
	json.Unmarshal([]byte(tagsJSON), &p.Tags)
	json.Unmarshal([]byte(metaJSON), &p.Metadata)
	return p, nil
}

func (s *Store) GetPromptVersion(name string, version int) (*PromptVersion, error) {
	p := &PromptVersion{}
	var varsJSON, tagsJSON, metaJSON string
	var active int
	err := s.db.QueryRow(
		`SELECT id,name,version,content,variables,model,temperature,max_tokens,tags,metadata,active,created_by,created_at
		 FROM prompt_versions WHERE name=? AND version=?`, name, version,
	).Scan(&p.ID, &p.Name, &p.Version, &p.Content, &varsJSON, &p.Model,
		&p.Temperature, &p.MaxTokens, &tagsJSON, &metaJSON, &active, &p.CreatedBy, &p.CreatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	p.Active = active != 0
	json.Unmarshal([]byte(varsJSON), &p.Variables)
	json.Unmarshal([]byte(tagsJSON), &p.Tags)
	json.Unmarshal([]byte(metaJSON), &p.Metadata)
	return p, nil
}

func (s *Store) ListPrompts(name string) ([]PromptVersion, error) {
	q := `SELECT id,name,version,content,variables,model,temperature,max_tokens,tags,metadata,active,created_by,created_at
	      FROM prompt_versions`
	var args []interface{}
	if name != "" { q += " WHERE name=?"; args = append(args, name) }
	q += " ORDER BY name, version DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []PromptVersion
	for rows.Next() {
		var p PromptVersion
		var varsJSON, tagsJSON, metaJSON string
		var active int
		rows.Scan(&p.ID, &p.Name, &p.Version, &p.Content, &varsJSON, &p.Model,
			&p.Temperature, &p.MaxTokens, &tagsJSON, &metaJSON, &active, &p.CreatedBy, &p.CreatedAt)
		p.Active = active != 0
		json.Unmarshal([]byte(varsJSON), &p.Variables)
		json.Unmarshal([]byte(tagsJSON), &p.Tags)
		json.Unmarshal([]byte(metaJSON), &p.Metadata)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) ActivatePrompt(name string, version int) error {
	// Deactivate all versions of this prompt
	s.db.Exec("UPDATE prompt_versions SET active=0 WHERE name=?", name)
	// Activate the specified version
	_, err := s.db.Exec("UPDATE prompt_versions SET active=1 WHERE name=? AND version=?", name, version)
	return err
}

// --- Strategies ---

func (s *Store) CreateStrategy(st *Strategy) (*Strategy, error) {
	st.ID = newID("strat_")
	now := time.Now().UTC().Format(time.RFC3339)
	st.CreatedAt = now
	st.ModifiedAt = now
	if st.Rules == nil { st.Rules = []interface{}{} }
	if st.Metadata == nil { st.Metadata = map[string]interface{}{} }
	if st.StrategyType == "" { st.StrategyType = "general" }

	rulesJSON, _ := json.Marshal(st.Rules)
	metaJSON, _ := json.Marshal(st.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO strategies (id,name,description,strategy_type,rules,priority,enabled,agent_id,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.Name, st.Description, st.StrategyType,
		string(rulesJSON), st.Priority, boolToInt(st.Enabled), st.AgentID,
		string(metaJSON), now, now,
	)
	return st, err
}

func (s *Store) GetStrategy(id string) (*Strategy, error) {
	st := &Strategy{}
	var rulesJSON, metaJSON string
	var enabled int
	err := s.db.QueryRow(
		`SELECT id,name,description,strategy_type,rules,priority,enabled,agent_id,metadata,created_at,modified_at
		 FROM strategies WHERE id=?`, id,
	).Scan(&st.ID, &st.Name, &st.Description, &st.StrategyType,
		&rulesJSON, &st.Priority, &enabled, &st.AgentID, &metaJSON, &st.CreatedAt, &st.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	st.Enabled = enabled != 0
	json.Unmarshal([]byte(rulesJSON), &st.Rules)
	json.Unmarshal([]byte(metaJSON), &st.Metadata)
	return st, nil
}

func (s *Store) ListStrategies(strategyType, agentID string) ([]Strategy, error) {
	q := `SELECT id,name,description,strategy_type,rules,priority,enabled,agent_id,metadata,created_at,modified_at
	      FROM strategies WHERE enabled=1`
	var args []interface{}
	if strategyType != "" { q += " AND strategy_type=?"; args = append(args, strategyType) }
	if agentID != "" { q += " AND (agent_id=? OR agent_id='')"; args = append(args, agentID) }
	q += " ORDER BY priority DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Strategy
	for rows.Next() {
		var st Strategy
		var rulesJSON, metaJSON string
		var enabled int
		rows.Scan(&st.ID, &st.Name, &st.Description, &st.StrategyType,
			&rulesJSON, &st.Priority, &enabled, &st.AgentID, &metaJSON, &st.CreatedAt, &st.ModifiedAt)
		st.Enabled = enabled != 0
		json.Unmarshal([]byte(rulesJSON), &st.Rules)
		json.Unmarshal([]byte(metaJSON), &st.Metadata)
		result = append(result, st)
	}
	return result, rows.Err()
}

func (s *Store) UpdateStrategy(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "description", "strategy_type", "agent_id":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "priority":
			sets = append(sets, "priority=?"); args = append(args, v)
		case "enabled":
			b, _ := v.(bool); sets = append(sets, "enabled=?"); args = append(args, boolToInt(b))
		case "rules", "metadata":
			j, _ := json.Marshal(v); sets = append(sets, k+"=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE strategies SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func boolToInt(b bool) int {
	if b { return 1 }
	return 0
}
