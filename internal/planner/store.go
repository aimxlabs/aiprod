package planner

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
	`CREATE TABLE IF NOT EXISTS plans (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		goal        TEXT DEFAULT '',
		status      TEXT DEFAULT 'draft',
		priority    INTEGER DEFAULT 0,
		parent_id   TEXT DEFAULT '',
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_plan_agent ON plans(agent_id);
	CREATE INDEX IF NOT EXISTS idx_plan_status ON plans(status);
	CREATE INDEX IF NOT EXISTS idx_plan_parent ON plans(parent_id) WHERE parent_id != '';

	CREATE TABLE IF NOT EXISTS plan_steps (
		id          TEXT PRIMARY KEY,
		plan_id     TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
		seq         INTEGER NOT NULL,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		action      TEXT DEFAULT '',
		status      TEXT DEFAULT 'pending',
		depends_on  TEXT DEFAULT '[]',
		output      TEXT DEFAULT '',
		error       TEXT DEFAULT '',
		tool_id     TEXT DEFAULT '',
		estimated_ms INTEGER DEFAULT 0,
		actual_ms   INTEGER DEFAULT 0,
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_pstep_plan ON plan_steps(plan_id, seq);
	CREATE INDEX IF NOT EXISTS idx_pstep_status ON plan_steps(status);

	CREATE TABLE IF NOT EXISTS reflections (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		source_type TEXT NOT NULL,
		source_id   TEXT DEFAULT '',
		reflection_type TEXT DEFAULT 'post_mortem',
		content     TEXT NOT NULL,
		lessons     TEXT DEFAULT '[]',
		score       REAL DEFAULT 0.0,
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_refl_agent ON reflections(agent_id);
	CREATE INDEX IF NOT EXISTS idx_refl_source ON reflections(source_type, source_id);`,
}

type Store struct{ db *sql.DB }

type Plan struct {
	ID          string                 `json:"id"`
	AgentID     string                 `json:"agent_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Goal        string                 `json:"goal"`
	Status      string                 `json:"status"`
	Priority    int                    `json:"priority"`
	ParentID    string                 `json:"parent_id,omitempty"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
	Steps       []PlanStep             `json:"steps,omitempty"`
}

type PlanStep struct {
	ID          string                 `json:"id"`
	PlanID      string                 `json:"plan_id"`
	Seq         int                    `json:"seq"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Action      string                 `json:"action"`
	Status      string                 `json:"status"`
	DependsOn   []string               `json:"depends_on"`
	Output      string                 `json:"output,omitempty"`
	Error       string                 `json:"error,omitempty"`
	ToolID      string                 `json:"tool_id,omitempty"`
	EstimatedMs int64                  `json:"estimated_ms"`
	ActualMs    int64                  `json:"actual_ms"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
}

type Reflection struct {
	ID             string                 `json:"id"`
	AgentID        string                 `json:"agent_id"`
	SourceType     string                 `json:"source_type"`
	SourceID       string                 `json:"source_id,omitempty"`
	ReflectionType string                 `json:"reflection_type"`
	Content        string                 `json:"content"`
	Lessons        []string               `json:"lessons"`
	Score          float64                `json:"score"`
	Tags           []string               `json:"tags"`
	Metadata       map[string]interface{} `json:"metadata"`
	CreatedAt      string                 `json:"created_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "planner", migrations); err != nil {
		return nil, fmt.Errorf("migrating planner schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Plans ---

func (s *Store) CreatePlan(p *Plan) (*Plan, error) {
	p.ID = newID("plan_")
	now := time.Now().UTC().Format(time.RFC3339)
	p.CreatedAt = now
	p.ModifiedAt = now
	if p.Status == "" { p.Status = "draft" }
	if p.Tags == nil { p.Tags = []string{} }
	if p.Metadata == nil { p.Metadata = map[string]interface{}{} }

	tagsJSON, _ := json.Marshal(p.Tags)
	metaJSON, _ := json.Marshal(p.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO plans (id,agent_id,name,description,goal,status,priority,parent_id,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.AgentID, p.Name, p.Description, p.Goal, p.Status, p.Priority,
		p.ParentID, string(tagsJSON), string(metaJSON), now, now,
	)
	return p, err
}

func (s *Store) GetPlan(id string, includeSteps bool) (*Plan, error) {
	p := &Plan{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,agent_id,name,description,goal,status,priority,COALESCE(parent_id,''),tags,metadata,created_at,modified_at
		 FROM plans WHERE id=?`, id,
	).Scan(&p.ID, &p.AgentID, &p.Name, &p.Description, &p.Goal, &p.Status, &p.Priority,
		&p.ParentID, &tagsJSON, &metaJSON, &p.CreatedAt, &p.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(tagsJSON), &p.Tags)
	json.Unmarshal([]byte(metaJSON), &p.Metadata)

	if includeSteps {
		p.Steps, _ = s.GetSteps(id)
	}
	return p, nil
}

type PlanListOpts struct {
	AgentID  string
	Status   string
	ParentID string
	Limit    int
}

func (s *Store) ListPlans(opts PlanListOpts) ([]Plan, error) {
	q := `SELECT id,agent_id,name,description,goal,status,priority,COALESCE(parent_id,''),tags,metadata,created_at,modified_at
	      FROM plans WHERE 1=1`
	var args []interface{}
	if opts.AgentID != "" { q += " AND agent_id=?"; args = append(args, opts.AgentID) }
	if opts.Status != "" { q += " AND status=?"; args = append(args, opts.Status) }
	if opts.ParentID != "" { q += " AND parent_id=?"; args = append(args, opts.ParentID) }
	q += " ORDER BY priority DESC, modified_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Plan
	for rows.Next() {
		var p Plan
		var tagsJSON, metaJSON string
		rows.Scan(&p.ID, &p.AgentID, &p.Name, &p.Description, &p.Goal, &p.Status, &p.Priority,
			&p.ParentID, &tagsJSON, &metaJSON, &p.CreatedAt, &p.ModifiedAt)
		json.Unmarshal([]byte(tagsJSON), &p.Tags)
		json.Unmarshal([]byte(metaJSON), &p.Metadata)
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *Store) UpdatePlan(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "description", "goal", "status", "parent_id":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "priority":
			sets = append(sets, "priority=?"); args = append(args, v)
		case "tags":
			j, _ := json.Marshal(v); sets = append(sets, "tags=?"); args = append(args, string(j))
		case "metadata":
			j, _ := json.Marshal(v); sets = append(sets, "metadata=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE plans SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) DeletePlan(id string) error {
	_, err := s.db.Exec("DELETE FROM plans WHERE id=?", id)
	return err
}

// --- Plan Steps ---

func (s *Store) AddStep(step *PlanStep) (*PlanStep, error) {
	step.ID = newID("pstep_")
	now := time.Now().UTC().Format(time.RFC3339)
	step.CreatedAt = now
	step.ModifiedAt = now
	if step.Status == "" { step.Status = "pending" }
	if step.DependsOn == nil { step.DependsOn = []string{} }
	if step.Metadata == nil { step.Metadata = map[string]interface{}{} }

	if step.Seq == 0 {
		s.db.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM plan_steps WHERE plan_id=?", step.PlanID).Scan(&step.Seq)
	}

	depsJSON, _ := json.Marshal(step.DependsOn)
	metaJSON, _ := json.Marshal(step.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO plan_steps (id,plan_id,seq,name,description,action,status,depends_on,tool_id,estimated_ms,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		step.ID, step.PlanID, step.Seq, step.Name, step.Description, step.Action,
		step.Status, string(depsJSON), step.ToolID, step.EstimatedMs, string(metaJSON), now, now,
	)
	return step, err
}

func (s *Store) GetSteps(planID string) ([]PlanStep, error) {
	rows, err := s.db.Query(
		`SELECT id,plan_id,seq,name,description,action,status,depends_on,output,error,tool_id,estimated_ms,actual_ms,metadata,created_at,modified_at
		 FROM plan_steps WHERE plan_id=? ORDER BY seq`, planID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []PlanStep
	for rows.Next() {
		var st PlanStep
		var depsJSON, metaJSON string
		rows.Scan(&st.ID, &st.PlanID, &st.Seq, &st.Name, &st.Description, &st.Action, &st.Status,
			&depsJSON, &st.Output, &st.Error, &st.ToolID, &st.EstimatedMs, &st.ActualMs,
			&metaJSON, &st.CreatedAt, &st.ModifiedAt)
		json.Unmarshal([]byte(depsJSON), &st.DependsOn)
		json.Unmarshal([]byte(metaJSON), &st.Metadata)
		result = append(result, st)
	}
	return result, rows.Err()
}

func (s *Store) UpdateStep(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "description", "action", "status", "output", "error", "tool_id":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "actual_ms", "estimated_ms":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "depends_on":
			j, _ := json.Marshal(v); sets = append(sets, "depends_on=?"); args = append(args, string(j))
		case "metadata":
			j, _ := json.Marshal(v); sets = append(sets, "metadata=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE plan_steps SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

// --- Reflections ---

func (s *Store) CreateReflection(r *Reflection) (*Reflection, error) {
	r.ID = newID("refl_")
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if r.ReflectionType == "" { r.ReflectionType = "post_mortem" }
	if r.Lessons == nil { r.Lessons = []string{} }
	if r.Tags == nil { r.Tags = []string{} }
	if r.Metadata == nil { r.Metadata = map[string]interface{}{} }

	lessonsJSON, _ := json.Marshal(r.Lessons)
	tagsJSON, _ := json.Marshal(r.Tags)
	metaJSON, _ := json.Marshal(r.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO reflections (id,agent_id,source_type,source_id,reflection_type,content,lessons,score,tags,metadata,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.AgentID, r.SourceType, r.SourceID, r.ReflectionType,
		r.Content, string(lessonsJSON), r.Score, string(tagsJSON), string(metaJSON), r.CreatedAt,
	)
	return r, err
}

func (s *Store) GetReflection(id string) (*Reflection, error) {
	r := &Reflection{}
	var lessonsJSON, tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,agent_id,source_type,source_id,reflection_type,content,lessons,score,tags,metadata,created_at
		 FROM reflections WHERE id=?`, id,
	).Scan(&r.ID, &r.AgentID, &r.SourceType, &r.SourceID, &r.ReflectionType,
		&r.Content, &lessonsJSON, &r.Score, &tagsJSON, &metaJSON, &r.CreatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(lessonsJSON), &r.Lessons)
	json.Unmarshal([]byte(tagsJSON), &r.Tags)
	json.Unmarshal([]byte(metaJSON), &r.Metadata)
	return r, nil
}

type ReflectionListOpts struct {
	AgentID        string
	SourceType     string
	SourceID       string
	ReflectionType string
	Limit          int
}

func (s *Store) ListReflections(opts ReflectionListOpts) ([]Reflection, error) {
	q := `SELECT id,agent_id,source_type,source_id,reflection_type,content,lessons,score,tags,metadata,created_at
	      FROM reflections WHERE 1=1`
	var args []interface{}
	if opts.AgentID != "" { q += " AND agent_id=?"; args = append(args, opts.AgentID) }
	if opts.SourceType != "" { q += " AND source_type=?"; args = append(args, opts.SourceType) }
	if opts.SourceID != "" { q += " AND source_id=?"; args = append(args, opts.SourceID) }
	if opts.ReflectionType != "" { q += " AND reflection_type=?"; args = append(args, opts.ReflectionType) }
	q += " ORDER BY created_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Reflection
	for rows.Next() {
		var r Reflection
		var lessonsJSON, tagsJSON, metaJSON string
		rows.Scan(&r.ID, &r.AgentID, &r.SourceType, &r.SourceID, &r.ReflectionType,
			&r.Content, &lessonsJSON, &r.Score, &tagsJSON, &metaJSON, &r.CreatedAt)
		json.Unmarshal([]byte(lessonsJSON), &r.Lessons)
		json.Unmarshal([]byte(tagsJSON), &r.Tags)
		json.Unmarshal([]byte(metaJSON), &r.Metadata)
		result = append(result, r)
	}
	return result, rows.Err()
}
