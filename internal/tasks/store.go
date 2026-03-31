package tasks

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
	`CREATE TABLE IF NOT EXISTS tasks (
		id          TEXT PRIMARY KEY,
		title       TEXT NOT NULL,
		description TEXT DEFAULT '',
		status      TEXT DEFAULT 'open',
		priority    TEXT DEFAULT 'medium',
		assignee    TEXT DEFAULT '',
		created_by  TEXT DEFAULT '',
		parent_id   TEXT DEFAULT '',
		due_date    TEXT DEFAULT '',
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS task_dependencies (
		task_id    TEXT NOT NULL REFERENCES tasks(id),
		depends_on TEXT NOT NULL REFERENCES tasks(id),
		dep_type   TEXT DEFAULT 'blocks',
		PRIMARY KEY (task_id, depends_on)
	);

	CREATE TABLE IF NOT EXISTS task_events (
		id         TEXT PRIMARY KEY,
		task_id    TEXT NOT NULL REFERENCES tasks(id),
		event_type TEXT NOT NULL,
		actor      TEXT DEFAULT '',
		old_value  TEXT DEFAULT '',
		new_value  TEXT DEFAULT '',
		comment    TEXT DEFAULT '',
		created_at TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
	CREATE INDEX IF NOT EXISTS idx_tasks_assignee ON tasks(assignee);
	CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id);`,

	// Migration 2: add agent_id column for per-agent scoping
	`ALTER TABLE tasks ADD COLUMN agent_id TEXT DEFAULT '';
	UPDATE tasks SET agent_id = created_by WHERE agent_id = '';
	CREATE INDEX IF NOT EXISTS idx_tasks_agent ON tasks(agent_id);`,
}

var validStatuses = map[string]bool{
	"open": true, "in_progress": true, "blocked": true,
	"review": true, "done": true, "cancelled": true,
}

var validTransitions = map[string][]string{
	"open":        {"in_progress", "cancelled"},
	"in_progress": {"review", "blocked", "done", "cancelled"},
	"blocked":     {"in_progress", "cancelled"},
	"review":      {"in_progress", "done", "cancelled"},
	"done":        {"open"}, // reopen
	"cancelled":   {"open"}, // reopen
}

type Store struct {
	db *sql.DB
}

type Task struct {
	ID          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	Priority    string                 `json:"priority"`
	Assignee    string                 `json:"assignee"`
	CreatedBy   string                 `json:"created_by"`
	AgentID     string                 `json:"agent_id"`
	ParentID    string                 `json:"parent_id,omitempty"`
	DueDate     string                 `json:"due_date,omitempty"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
}

type Event struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	EventType string `json:"event_type"`
	Actor     string `json:"actor"`
	OldValue  string `json:"old_value,omitempty"`
	NewValue  string `json:"new_value,omitempty"`
	Comment   string `json:"comment,omitempty"`
	CreatedAt string `json:"created_at"`
}

type Dependency struct {
	TaskID    string `json:"task_id"`
	DependsOn string `json:"depends_on"`
	DepType   string `json:"dep_type"`
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "tasks", migrations); err != nil {
		return nil, fmt.Errorf("migrating tasks schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Store) Create(title, description, priority, assignee, createdBy, agentID, parentID, dueDate string, tags []string, metadata map[string]interface{}) (*Task, error) {
	id := newID("task_")
	now := time.Now().UTC().Format(time.RFC3339)

	if priority == "" {
		priority = "medium"
	}
	if tags == nil {
		tags = []string{}
	}
	if metadata == nil {
		metadata = map[string]interface{}{}
	}

	tagsJSON, _ := json.Marshal(tags)
	metaJSON, _ := json.Marshal(metadata)

	if agentID == "" {
		agentID = createdBy
	}
	_, err := s.db.Exec(
		`INSERT INTO tasks (id, title, description, status, priority, assignee, created_by, agent_id, parent_id, due_date, tags, metadata, created_at, modified_at)
		 VALUES (?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, title, description, priority, assignee, createdBy, agentID, parentID, dueDate,
		string(tagsJSON), string(metaJSON), now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}

	s.addEvent(id, "created", createdBy, "", "open", "")

	return &Task{
		ID: id, Title: title, Description: description, Status: "open",
		Priority: priority, Assignee: assignee, CreatedBy: createdBy,
		AgentID: agentID, ParentID: parentID, DueDate: dueDate,
		Tags: tags, Metadata: metadata, CreatedAt: now, ModifiedAt: now,
	}, nil
}

func (s *Store) Get(id string) (*Task, error) {
	t := &Task{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id, title, description, status, priority, assignee, created_by, agent_id, parent_id, due_date, tags, metadata, created_at, modified_at
		 FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&t.CreatedBy, &t.AgentID, &t.ParentID, &t.DueDate, &tagsJSON, &metaJSON, &t.CreatedAt, &t.ModifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting task: %w", err)
	}
	json.Unmarshal([]byte(tagsJSON), &t.Tags)
	json.Unmarshal([]byte(metaJSON), &t.Metadata)
	return t, nil
}

type ListOptions struct {
	AgentID  string
	Status   string
	Assignee string
	Priority string
	Tag      string
	ParentID string
	Cursor   string
	Limit    int
}

func (s *Store) List(opts ListOptions) ([]Task, error) {
	query := `SELECT id, title, description, status, priority, assignee, created_by, agent_id, parent_id, due_date, tags, metadata, created_at, modified_at FROM tasks WHERE 1=1`
	var args []interface{}

	if opts.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, opts.AgentID)
	}
	if opts.Status != "" {
		query += " AND status = ?"
		args = append(args, opts.Status)
	}
	if opts.Assignee != "" {
		query += " AND assignee = ?"
		args = append(args, opts.Assignee)
	}
	if opts.Priority != "" {
		query += " AND priority = ?"
		args = append(args, opts.Priority)
	}
	if opts.Tag != "" {
		query += " AND tags LIKE ?"
		args = append(args, "%\""+opts.Tag+"\"%")
	}
	if opts.ParentID != "" {
		query += " AND parent_id = ?"
		args = append(args, opts.ParentID)
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
		return nil, fmt.Errorf("listing tasks: %w", err)
	}
	defer rows.Close()

	var result []Task
	for rows.Next() {
		var t Task
		var tagsJSON, metaJSON string
		if err := rows.Scan(&t.ID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.CreatedBy, &t.AgentID, &t.ParentID, &t.DueDate, &tagsJSON, &metaJSON, &t.CreatedAt, &t.ModifiedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(tagsJSON), &t.Tags)
		json.Unmarshal([]byte(metaJSON), &t.Metadata)
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *Store) Update(id, actor string, updates map[string]interface{}) (*Task, error) {
	t, err := s.Get(id)
	if err != nil || t == nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	sets := []string{"modified_at = ?"}
	args := []interface{}{now}

	for k, v := range updates {
		switch k {
		case "title", "description", "priority", "assignee", "due_date":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		case "tags":
			tagsJSON, _ := json.Marshal(v)
			sets = append(sets, "tags = ?")
			args = append(args, string(tagsJSON))
		case "metadata":
			metaJSON, _ := json.Marshal(v)
			sets = append(sets, "metadata = ?")
			args = append(args, string(metaJSON))
		}
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?", joinStrings(sets, ", "))
	if _, err := s.db.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("updating task: %w", err)
	}

	return s.Get(id)
}

func (s *Store) Transition(id, actor, newStatus string) (*Task, error) {
	t, err := s.Get(id)
	if err != nil || t == nil {
		return nil, err
	}

	if !validStatuses[newStatus] {
		return nil, fmt.Errorf("invalid status: %s", newStatus)
	}

	allowed := validTransitions[t.Status]
	valid := false
	for _, s := range allowed {
		if s == newStatus {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("cannot transition from %s to %s", t.Status, newStatus)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec("UPDATE tasks SET status = ?, modified_at = ? WHERE id = ?", newStatus, now, id); err != nil {
		return nil, fmt.Errorf("transitioning task: %w", err)
	}

	s.addEvent(id, "status_change", actor, t.Status, newStatus, "")

	t.Status = newStatus
	t.ModifiedAt = now
	return t, nil
}

func (s *Store) AddComment(id, actor, comment string) error {
	s.addEvent(id, "comment", actor, "", "", comment)
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec("UPDATE tasks SET modified_at = ? WHERE id = ?", now, id)
	return nil
}

func (s *Store) GetEvents(taskID string) ([]Event, error) {
	rows, err := s.db.Query(
		"SELECT id, task_id, event_type, actor, old_value, new_value, comment, created_at FROM task_events WHERE task_id = ? ORDER BY created_at",
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("getting events: %w", err)
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TaskID, &e.EventType, &e.Actor, &e.OldValue, &e.NewValue, &e.Comment, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) AddDependency(taskID, dependsOn, depType string) error {
	if depType == "" {
		depType = "blocks"
	}
	_, err := s.db.Exec(
		"INSERT OR IGNORE INTO task_dependencies (task_id, depends_on, dep_type) VALUES (?, ?, ?)",
		taskID, dependsOn, depType,
	)
	return err
}

func (s *Store) GetDependencies(taskID string) ([]Dependency, error) {
	rows, err := s.db.Query(
		"SELECT task_id, depends_on, dep_type FROM task_dependencies WHERE task_id = ?", taskID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deps []Dependency
	for rows.Next() {
		var d Dependency
		if err := rows.Scan(&d.TaskID, &d.DependsOn, &d.DepType); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec("DELETE FROM task_events WHERE task_id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting task events: %w", err)
	}
	_, err = s.db.Exec("DELETE FROM task_dependencies WHERE task_id = ? OR depends_on = ?", id, id)
	if err != nil {
		return fmt.Errorf("deleting task dependencies: %w", err)
	}
	res, err := s.db.Exec("DELETE FROM tasks WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

func (s *Store) addEvent(taskID, eventType, actor, oldVal, newVal, comment string) {
	id := newID("evt_")
	now := time.Now().UTC().Format(time.RFC3339)
	s.db.Exec(
		"INSERT INTO task_events (id, task_id, event_type, actor, old_value, new_value, comment, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, taskID, eventType, actor, oldVal, newVal, comment, now,
	)
}

func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
