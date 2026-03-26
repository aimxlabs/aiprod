package taskgraph

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
	`CREATE TABLE IF NOT EXISTS dag_graphs (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		status      TEXT DEFAULT 'pending',
		plan_id     TEXT DEFAULT '',
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_dag_agent ON dag_graphs(agent_id);
	CREATE INDEX IF NOT EXISTS idx_dag_status ON dag_graphs(status);
	CREATE INDEX IF NOT EXISTS idx_dag_plan ON dag_graphs(plan_id) WHERE plan_id != '';

	CREATE TABLE IF NOT EXISTS dag_nodes (
		id          TEXT PRIMARY KEY,
		graph_id    TEXT NOT NULL REFERENCES dag_graphs(id) ON DELETE CASCADE,
		task_id     TEXT DEFAULT '',
		name        TEXT NOT NULL,
		node_type   TEXT DEFAULT 'action',
		status      TEXT DEFAULT 'pending',
		input       TEXT DEFAULT '{}',
		output      TEXT DEFAULT '',
		error       TEXT DEFAULT '',
		retries     INTEGER DEFAULT 0,
		max_retries INTEGER DEFAULT 3,
		timeout_ms  INTEGER DEFAULT 0,
		tool_id     TEXT DEFAULT '',
		metadata    TEXT DEFAULT '{}',
		started_at  TEXT DEFAULT '',
		ended_at    TEXT DEFAULT '',
		duration_ms INTEGER DEFAULT 0,
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_dnode_graph ON dag_nodes(graph_id);
	CREATE INDEX IF NOT EXISTS idx_dnode_status ON dag_nodes(status);
	CREATE INDEX IF NOT EXISTS idx_dnode_task ON dag_nodes(task_id) WHERE task_id != '';

	CREATE TABLE IF NOT EXISTS dag_edges (
		id        TEXT PRIMARY KEY,
		graph_id  TEXT NOT NULL REFERENCES dag_graphs(id) ON DELETE CASCADE,
		from_node TEXT NOT NULL REFERENCES dag_nodes(id),
		to_node   TEXT NOT NULL REFERENCES dag_nodes(id),
		edge_type TEXT DEFAULT 'depends_on',
		condition TEXT DEFAULT '',
		metadata  TEXT DEFAULT '{}'
	);
	CREATE INDEX IF NOT EXISTS idx_dedge_graph ON dag_edges(graph_id);
	CREATE INDEX IF NOT EXISTS idx_dedge_from ON dag_edges(from_node);
	CREATE INDEX IF NOT EXISTS idx_dedge_to ON dag_edges(to_node);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_dedge_pair ON dag_edges(from_node, to_node);`,
}

type Store struct{ db *sql.DB }

type Graph struct {
	ID          string                 `json:"id"`
	AgentID     string                 `json:"agent_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	PlanID      string                 `json:"plan_id,omitempty"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
	Nodes       []Node                 `json:"nodes,omitempty"`
	Edges       []Edge                 `json:"edges,omitempty"`
}

type Node struct {
	ID         string                 `json:"id"`
	GraphID    string                 `json:"graph_id"`
	TaskID     string                 `json:"task_id,omitempty"`
	Name       string                 `json:"name"`
	NodeType   string                 `json:"node_type"`
	Status     string                 `json:"status"`
	Input      map[string]interface{} `json:"input"`
	Output     string                 `json:"output,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Retries    int                    `json:"retries"`
	MaxRetries int                    `json:"max_retries"`
	TimeoutMs  int64                  `json:"timeout_ms"`
	ToolID     string                 `json:"tool_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata"`
	StartedAt  string                 `json:"started_at,omitempty"`
	EndedAt    string                 `json:"ended_at,omitempty"`
	DurationMs int64                  `json:"duration_ms"`
	CreatedAt  string                 `json:"created_at"`
	ModifiedAt string                 `json:"modified_at"`
}

type Edge struct {
	ID        string                 `json:"id"`
	GraphID   string                 `json:"graph_id"`
	FromNode  string                 `json:"from_node"`
	ToNode    string                 `json:"to_node"`
	EdgeType  string                 `json:"edge_type"`
	Condition string                 `json:"condition,omitempty"`
	Metadata  map[string]interface{} `json:"metadata"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "taskgraph", migrations); err != nil {
		return nil, fmt.Errorf("migrating taskgraph schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Graphs ---

func (s *Store) CreateGraph(g *Graph) (*Graph, error) {
	g.ID = newID("dag_")
	now := time.Now().UTC().Format(time.RFC3339)
	g.CreatedAt = now
	g.ModifiedAt = now
	if g.Status == "" { g.Status = "pending" }
	if g.Tags == nil { g.Tags = []string{} }
	if g.Metadata == nil { g.Metadata = map[string]interface{}{} }

	tagsJSON, _ := json.Marshal(g.Tags)
	metaJSON, _ := json.Marshal(g.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO dag_graphs (id,agent_id,name,description,status,plan_id,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		g.ID, g.AgentID, g.Name, g.Description, g.Status, g.PlanID,
		string(tagsJSON), string(metaJSON), now, now,
	)
	return g, err
}

func (s *Store) GetGraph(id string, includeNodes bool) (*Graph, error) {
	g := &Graph{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,agent_id,name,description,status,COALESCE(plan_id,''),tags,metadata,created_at,modified_at
		 FROM dag_graphs WHERE id=?`, id,
	).Scan(&g.ID, &g.AgentID, &g.Name, &g.Description, &g.Status, &g.PlanID,
		&tagsJSON, &metaJSON, &g.CreatedAt, &g.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(tagsJSON), &g.Tags)
	json.Unmarshal([]byte(metaJSON), &g.Metadata)

	if includeNodes {
		g.Nodes, _ = s.GetNodes(id)
		g.Edges, _ = s.GetEdges(id)
	}
	return g, nil
}

func (s *Store) ListGraphs(agentID, status string, limit int) ([]Graph, error) {
	q := `SELECT id,agent_id,name,description,status,COALESCE(plan_id,''),tags,metadata,created_at,modified_at
	      FROM dag_graphs WHERE 1=1`
	var args []interface{}
	if agentID != "" { q += " AND agent_id=?"; args = append(args, agentID) }
	if status != "" { q += " AND status=?"; args = append(args, status) }
	q += " ORDER BY modified_at DESC"
	if limit > 0 { q += fmt.Sprintf(" LIMIT %d", limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Graph
	for rows.Next() {
		var g Graph
		var tagsJSON, metaJSON string
		rows.Scan(&g.ID, &g.AgentID, &g.Name, &g.Description, &g.Status, &g.PlanID,
			&tagsJSON, &metaJSON, &g.CreatedAt, &g.ModifiedAt)
		json.Unmarshal([]byte(tagsJSON), &g.Tags)
		json.Unmarshal([]byte(metaJSON), &g.Metadata)
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s *Store) UpdateGraph(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "description", "status", "plan_id":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "tags":
			j, _ := json.Marshal(v); sets = append(sets, "tags=?"); args = append(args, string(j))
		case "metadata":
			j, _ := json.Marshal(v); sets = append(sets, "metadata=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE dag_graphs SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) DeleteGraph(id string) error {
	_, err := s.db.Exec("DELETE FROM dag_graphs WHERE id=?", id)
	return err
}

// --- Nodes ---

func (s *Store) AddNode(n *Node) (*Node, error) {
	n.ID = newID("node_")
	now := time.Now().UTC().Format(time.RFC3339)
	n.CreatedAt = now
	n.ModifiedAt = now
	if n.Status == "" { n.Status = "pending" }
	if n.NodeType == "" { n.NodeType = "action" }
	if n.MaxRetries == 0 { n.MaxRetries = 3 }
	if n.Input == nil { n.Input = map[string]interface{}{} }
	if n.Metadata == nil { n.Metadata = map[string]interface{}{} }

	inputJSON, _ := json.Marshal(n.Input)
	metaJSON, _ := json.Marshal(n.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO dag_nodes (id,graph_id,task_id,name,node_type,status,input,max_retries,timeout_ms,tool_id,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.GraphID, n.TaskID, n.Name, n.NodeType, n.Status,
		string(inputJSON), n.MaxRetries, n.TimeoutMs, n.ToolID, string(metaJSON), now, now,
	)
	return n, err
}

func (s *Store) GetNodes(graphID string) ([]Node, error) {
	rows, err := s.db.Query(
		`SELECT id,graph_id,task_id,name,node_type,status,input,output,error,retries,max_retries,timeout_ms,tool_id,metadata,
		        COALESCE(started_at,''),COALESCE(ended_at,''),duration_ms,created_at,modified_at
		 FROM dag_nodes WHERE graph_id=? ORDER BY created_at`, graphID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Node
	for rows.Next() {
		var n Node
		var inputJSON, metaJSON string
		rows.Scan(&n.ID, &n.GraphID, &n.TaskID, &n.Name, &n.NodeType, &n.Status,
			&inputJSON, &n.Output, &n.Error, &n.Retries, &n.MaxRetries, &n.TimeoutMs, &n.ToolID,
			&metaJSON, &n.StartedAt, &n.EndedAt, &n.DurationMs, &n.CreatedAt, &n.ModifiedAt)
		json.Unmarshal([]byte(inputJSON), &n.Input)
		json.Unmarshal([]byte(metaJSON), &n.Metadata)
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Store) UpdateNode(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "status", "output", "error", "tool_id", "task_id":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "retries", "duration_ms":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "started_at", "ended_at":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "input", "metadata":
			j, _ := json.Marshal(v); sets = append(sets, k+"=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE dag_nodes SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

// --- Edges ---

func (s *Store) AddEdge(e *Edge) (*Edge, error) {
	e.ID = newID("edge_")
	if e.EdgeType == "" { e.EdgeType = "depends_on" }
	if e.Metadata == nil { e.Metadata = map[string]interface{}{} }

	metaJSON, _ := json.Marshal(e.Metadata)
	_, err := s.db.Exec(
		"INSERT INTO dag_edges (id,graph_id,from_node,to_node,edge_type,condition,metadata) VALUES (?,?,?,?,?,?,?)",
		e.ID, e.GraphID, e.FromNode, e.ToNode, e.EdgeType, e.Condition, string(metaJSON),
	)
	return e, err
}

func (s *Store) GetEdges(graphID string) ([]Edge, error) {
	rows, err := s.db.Query(
		"SELECT id,graph_id,from_node,to_node,edge_type,condition,metadata FROM dag_edges WHERE graph_id=?", graphID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Edge
	for rows.Next() {
		var e Edge
		var metaJSON string
		rows.Scan(&e.ID, &e.GraphID, &e.FromNode, &e.ToNode, &e.EdgeType, &e.Condition, &metaJSON)
		json.Unmarshal([]byte(metaJSON), &e.Metadata)
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Store) RemoveEdge(id string) error {
	_, err := s.db.Exec("DELETE FROM dag_edges WHERE id=?", id)
	return err
}

// ReadyNodes returns nodes whose dependencies are all completed.
func (s *Store) ReadyNodes(graphID string) ([]Node, error) {
	rows, err := s.db.Query(`
		SELECT n.id,n.graph_id,n.task_id,n.name,n.node_type,n.status,n.input,n.output,n.error,
		       n.retries,n.max_retries,n.timeout_ms,n.tool_id,n.metadata,
		       COALESCE(n.started_at,''),COALESCE(n.ended_at,''),n.duration_ms,n.created_at,n.modified_at
		FROM dag_nodes n
		WHERE n.graph_id=? AND n.status='pending'
		AND NOT EXISTS (
			SELECT 1 FROM dag_edges e
			JOIN dag_nodes dep ON dep.id = e.from_node
			WHERE e.to_node = n.id AND dep.status != 'completed'
		)`, graphID)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Node
	for rows.Next() {
		var n Node
		var inputJSON, metaJSON string
		rows.Scan(&n.ID, &n.GraphID, &n.TaskID, &n.Name, &n.NodeType, &n.Status,
			&inputJSON, &n.Output, &n.Error, &n.Retries, &n.MaxRetries, &n.TimeoutMs, &n.ToolID,
			&metaJSON, &n.StartedAt, &n.EndedAt, &n.DurationMs, &n.CreatedAt, &n.ModifiedAt)
		json.Unmarshal([]byte(inputJSON), &n.Input)
		json.Unmarshal([]byte(metaJSON), &n.Metadata)
		result = append(result, n)
	}
	return result, rows.Err()
}
