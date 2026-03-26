package knowledge

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
	`CREATE TABLE IF NOT EXISTS facts (
		id            TEXT PRIMARY KEY,
		agent_id      TEXT NOT NULL,
		subject       TEXT NOT NULL,
		predicate     TEXT NOT NULL,
		object        TEXT NOT NULL,
		confidence    REAL DEFAULT 1.0,
		source_type   TEXT DEFAULT '',
		source_id     TEXT DEFAULT '',
		valid_from    TEXT DEFAULT '',
		valid_until   TEXT DEFAULT '',
		tags          TEXT DEFAULT '[]',
		metadata      TEXT DEFAULT '{}',
		created_at    TEXT NOT NULL,
		modified_at   TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_fact_agent ON facts(agent_id);
	CREATE INDEX IF NOT EXISTS idx_fact_subject ON facts(subject);
	CREATE INDEX IF NOT EXISTS idx_fact_predicate ON facts(predicate);
	CREATE INDEX IF NOT EXISTS idx_fact_spo ON facts(subject, predicate, object);

	CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(subject, predicate, object);
	CREATE TABLE IF NOT EXISTS facts_fts_map (fact_id TEXT PRIMARY KEY, rowid INTEGER UNIQUE);

	CREATE TABLE IF NOT EXISTS entities (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		entity_type TEXT NOT NULL,
		description TEXT DEFAULT '',
		properties  TEXT DEFAULT '{}',
		aliases     TEXT DEFAULT '[]',
		tags        TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_entity_type ON entities(entity_type);
	CREATE INDEX IF NOT EXISTS idx_entity_name ON entities(name);

	CREATE TABLE IF NOT EXISTS entity_relations (
		id            TEXT PRIMARY KEY,
		from_entity   TEXT NOT NULL REFERENCES entities(id),
		to_entity     TEXT NOT NULL REFERENCES entities(id),
		relation_type TEXT NOT NULL,
		weight        REAL DEFAULT 1.0,
		properties    TEXT DEFAULT '{}',
		created_at    TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_erel_from ON entity_relations(from_entity);
	CREATE INDEX IF NOT EXISTS idx_erel_to ON entity_relations(to_entity);
	CREATE INDEX IF NOT EXISTS idx_erel_type ON entity_relations(relation_type);

	CREATE TABLE IF NOT EXISTS schema_inferences (
		id            TEXT PRIMARY KEY,
		source_type   TEXT NOT NULL,
		source_id     TEXT DEFAULT '',
		inferred_schema TEXT NOT NULL,
		confidence    REAL DEFAULT 0.0,
		sample_count  INTEGER DEFAULT 0,
		field_stats   TEXT DEFAULT '{}',
		metadata      TEXT DEFAULT '{}',
		created_at    TEXT NOT NULL,
		modified_at   TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_sinf_source ON schema_inferences(source_type, source_id);`,
}

type Store struct{ db *sql.DB }

type Fact struct {
	ID         string                 `json:"id"`
	AgentID    string                 `json:"agent_id"`
	Subject    string                 `json:"subject"`
	Predicate  string                 `json:"predicate"`
	Object     string                 `json:"object"`
	Confidence float64                `json:"confidence"`
	SourceType string                 `json:"source_type,omitempty"`
	SourceID   string                 `json:"source_id,omitempty"`
	ValidFrom  string                 `json:"valid_from,omitempty"`
	ValidUntil string                 `json:"valid_until,omitempty"`
	Tags       []string               `json:"tags"`
	Metadata   map[string]interface{} `json:"metadata"`
	CreatedAt  string                 `json:"created_at"`
	ModifiedAt string                 `json:"modified_at"`
}

type Entity struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	EntityType  string                 `json:"entity_type"`
	Description string                 `json:"description"`
	Properties  map[string]interface{} `json:"properties"`
	Aliases     []string               `json:"aliases"`
	Tags        []string               `json:"tags"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
}

type EntityRelation struct {
	ID           string                 `json:"id"`
	FromEntity   string                 `json:"from_entity"`
	ToEntity     string                 `json:"to_entity"`
	RelationType string                 `json:"relation_type"`
	Weight       float64                `json:"weight"`
	Properties   map[string]interface{} `json:"properties"`
	CreatedAt    string                 `json:"created_at"`
}

type SchemaInference struct {
	ID             string                 `json:"id"`
	SourceType     string                 `json:"source_type"`
	SourceID       string                 `json:"source_id,omitempty"`
	InferredSchema string                 `json:"inferred_schema"`
	Confidence     float64                `json:"confidence"`
	SampleCount    int                    `json:"sample_count"`
	FieldStats     map[string]interface{} `json:"field_stats"`
	Metadata       map[string]interface{} `json:"metadata"`
	CreatedAt      string                 `json:"created_at"`
	ModifiedAt     string                 `json:"modified_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "knowledge", migrations); err != nil {
		return nil, fmt.Errorf("migrating knowledge schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Facts ---

func (s *Store) CreateFact(f *Fact) (*Fact, error) {
	f.ID = newID("fact_")
	now := time.Now().UTC().Format(time.RFC3339)
	f.CreatedAt = now
	f.ModifiedAt = now
	if f.Confidence == 0 { f.Confidence = 1.0 }
	if f.Tags == nil { f.Tags = []string{} }
	if f.Metadata == nil { f.Metadata = map[string]interface{}{} }

	tagsJSON, _ := json.Marshal(f.Tags)
	metaJSON, _ := json.Marshal(f.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO facts (id,agent_id,subject,predicate,object,confidence,source_type,source_id,valid_from,valid_until,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.AgentID, f.Subject, f.Predicate, f.Object, f.Confidence,
		f.SourceType, f.SourceID, f.ValidFrom, f.ValidUntil,
		string(tagsJSON), string(metaJSON), now, now,
	)
	if err != nil { return nil, err }
	s.indexFactFTS(f.ID, f.Subject, f.Predicate, f.Object)
	return f, nil
}

func (s *Store) GetFact(id string) (*Fact, error) {
	f := &Fact{}
	var tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,agent_id,subject,predicate,object,confidence,source_type,source_id,
		        COALESCE(valid_from,''),COALESCE(valid_until,''),tags,metadata,created_at,modified_at
		 FROM facts WHERE id=?`, id,
	).Scan(&f.ID, &f.AgentID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence,
		&f.SourceType, &f.SourceID, &f.ValidFrom, &f.ValidUntil, &tagsJSON, &metaJSON, &f.CreatedAt, &f.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(tagsJSON), &f.Tags)
	json.Unmarshal([]byte(metaJSON), &f.Metadata)
	return f, nil
}

type FactListOpts struct {
	AgentID   string
	Subject   string
	Predicate string
	Query     string
	MinConf   float64
	Limit     int
}

func (s *Store) ListFacts(opts FactListOpts) ([]Fact, error) {
	if opts.Query != "" {
		return s.searchFacts(opts.Query, opts.AgentID, opts.Limit)
	}
	q := `SELECT id,agent_id,subject,predicate,object,confidence,source_type,source_id,
	      COALESCE(valid_from,''),COALESCE(valid_until,''),tags,metadata,created_at,modified_at
	      FROM facts WHERE 1=1`
	var args []interface{}
	if opts.AgentID != "" { q += " AND agent_id=?"; args = append(args, opts.AgentID) }
	if opts.Subject != "" { q += " AND subject=?"; args = append(args, opts.Subject) }
	if opts.Predicate != "" { q += " AND predicate=?"; args = append(args, opts.Predicate) }
	if opts.MinConf > 0 { q += " AND confidence>=?"; args = append(args, opts.MinConf) }
	q += " ORDER BY confidence DESC, modified_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	return s.scanFacts(q, args...)
}

func (s *Store) searchFacts(query, agentID string, limit int) ([]Fact, error) {
	if limit <= 0 { limit = 20 }
	rows, err := s.db.Query(
		"SELECT fm.fact_id FROM facts_fts f JOIN facts_fts_map fm ON fm.rowid = f.rowid WHERE facts_fts MATCH ? LIMIT ?", query, limit)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Fact
	for rows.Next() {
		var id string
		rows.Scan(&id)
		f, _ := s.GetFact(id)
		if f != nil && (agentID == "" || f.AgentID == agentID) { result = append(result, *f) }
	}
	return result, nil
}

func (s *Store) UpdateFact(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "subject", "predicate", "object", "source_type", "source_id", "valid_from", "valid_until":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "confidence":
			sets = append(sets, "confidence=?"); args = append(args, v)
		case "tags":
			j, _ := json.Marshal(v); sets = append(sets, "tags=?"); args = append(args, string(j))
		case "metadata":
			j, _ := json.Marshal(v); sets = append(sets, "metadata=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE facts SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) DeleteFact(id string) error {
	s.db.Exec("DELETE FROM facts_fts_map WHERE fact_id=?", id)
	_, err := s.db.Exec("DELETE FROM facts WHERE id=?", id)
	return err
}

func (s *Store) scanFacts(query string, args ...interface{}) ([]Fact, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Fact
	for rows.Next() {
		var f Fact
		var tagsJSON, metaJSON string
		rows.Scan(&f.ID, &f.AgentID, &f.Subject, &f.Predicate, &f.Object, &f.Confidence,
			&f.SourceType, &f.SourceID, &f.ValidFrom, &f.ValidUntil, &tagsJSON, &metaJSON, &f.CreatedAt, &f.ModifiedAt)
		json.Unmarshal([]byte(tagsJSON), &f.Tags)
		json.Unmarshal([]byte(metaJSON), &f.Metadata)
		result = append(result, f)
	}
	return result, rows.Err()
}

func (s *Store) indexFactFTS(id, subject, predicate, object string) {
	var rowID int64
	if s.db.QueryRow("SELECT rowid FROM facts_fts_map WHERE fact_id=?", id).Scan(&rowID) == nil {
		s.db.Exec("DELETE FROM facts_fts WHERE rowid=?", rowID)
		s.db.Exec("DELETE FROM facts_fts_map WHERE fact_id=?", id)
	}
	if res, err := s.db.Exec("INSERT INTO facts_fts (subject, predicate, object) VALUES (?,?,?)", subject, predicate, object); err == nil {
		r, _ := res.LastInsertId()
		s.db.Exec("INSERT INTO facts_fts_map (fact_id, rowid) VALUES (?,?)", id, r)
	}
}

// --- Entities ---

func (s *Store) CreateEntity(e *Entity) (*Entity, error) {
	e.ID = newID("ent_")
	now := time.Now().UTC().Format(time.RFC3339)
	e.CreatedAt = now
	e.ModifiedAt = now
	if e.Properties == nil { e.Properties = map[string]interface{}{} }
	if e.Aliases == nil { e.Aliases = []string{} }
	if e.Tags == nil { e.Tags = []string{} }
	if e.Metadata == nil { e.Metadata = map[string]interface{}{} }

	propsJSON, _ := json.Marshal(e.Properties)
	aliasesJSON, _ := json.Marshal(e.Aliases)
	tagsJSON, _ := json.Marshal(e.Tags)
	metaJSON, _ := json.Marshal(e.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO entities (id,name,entity_type,description,properties,aliases,tags,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.Name, e.EntityType, e.Description,
		string(propsJSON), string(aliasesJSON), string(tagsJSON), string(metaJSON), now, now,
	)
	return e, err
}

func (s *Store) GetEntity(id string) (*Entity, error) {
	e := &Entity{}
	var propsJSON, aliasesJSON, tagsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,name,entity_type,description,properties,aliases,tags,metadata,created_at,modified_at
		 FROM entities WHERE id=?`, id,
	).Scan(&e.ID, &e.Name, &e.EntityType, &e.Description,
		&propsJSON, &aliasesJSON, &tagsJSON, &metaJSON, &e.CreatedAt, &e.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(propsJSON), &e.Properties)
	json.Unmarshal([]byte(aliasesJSON), &e.Aliases)
	json.Unmarshal([]byte(tagsJSON), &e.Tags)
	json.Unmarshal([]byte(metaJSON), &e.Metadata)
	return e, nil
}

func (s *Store) ListEntities(entityType, query string, limit int) ([]Entity, error) {
	q := `SELECT id,name,entity_type,description,properties,aliases,tags,metadata,created_at,modified_at
	      FROM entities WHERE 1=1`
	var args []interface{}
	if entityType != "" { q += " AND entity_type=?"; args = append(args, entityType) }
	if query != "" { q += " AND (name LIKE ? OR description LIKE ?)"; args = append(args, "%"+query+"%", "%"+query+"%") }
	q += " ORDER BY name"
	if limit > 0 { q += fmt.Sprintf(" LIMIT %d", limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Entity
	for rows.Next() {
		var e Entity
		var propsJSON, aliasesJSON, tagsJSON, metaJSON string
		rows.Scan(&e.ID, &e.Name, &e.EntityType, &e.Description,
			&propsJSON, &aliasesJSON, &tagsJSON, &metaJSON, &e.CreatedAt, &e.ModifiedAt)
		json.Unmarshal([]byte(propsJSON), &e.Properties)
		json.Unmarshal([]byte(aliasesJSON), &e.Aliases)
		json.Unmarshal([]byte(tagsJSON), &e.Tags)
		json.Unmarshal([]byte(metaJSON), &e.Metadata)
		result = append(result, e)
	}
	return result, rows.Err()
}

func (s *Store) UpdateEntity(id string, updates map[string]interface{}) error {
	sets := []string{"modified_at=?"}
	args := []interface{}{time.Now().UTC().Format(time.RFC3339)}
	for k, v := range updates {
		switch k {
		case "name", "entity_type", "description":
			sets = append(sets, k+"=?"); args = append(args, v)
		case "properties", "metadata":
			j, _ := json.Marshal(v); sets = append(sets, k+"=?"); args = append(args, string(j))
		case "aliases", "tags":
			j, _ := json.Marshal(v); sets = append(sets, k+"=?"); args = append(args, string(j))
		}
	}
	args = append(args, id)
	q := "UPDATE entities SET "
	for i, sv := range sets { if i > 0 { q += ", " }; q += sv }
	q += " WHERE id=?"
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *Store) DeleteEntity(id string) error {
	s.db.Exec("DELETE FROM entity_relations WHERE from_entity=? OR to_entity=?", id, id)
	_, err := s.db.Exec("DELETE FROM entities WHERE id=?", id)
	return err
}

// --- Entity Relations ---

func (s *Store) AddRelation(r *EntityRelation) (*EntityRelation, error) {
	r.ID = newID("erel_")
	r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if r.Weight == 0 { r.Weight = 1.0 }
	if r.Properties == nil { r.Properties = map[string]interface{}{} }

	propsJSON, _ := json.Marshal(r.Properties)
	_, err := s.db.Exec(
		"INSERT INTO entity_relations (id,from_entity,to_entity,relation_type,weight,properties,created_at) VALUES (?,?,?,?,?,?,?)",
		r.ID, r.FromEntity, r.ToEntity, r.RelationType, r.Weight, string(propsJSON), r.CreatedAt,
	)
	return r, err
}

func (s *Store) GetRelations(entityID, direction string) ([]EntityRelation, error) {
	var q string
	switch direction {
	case "outgoing":
		q = "SELECT id,from_entity,to_entity,relation_type,weight,properties,created_at FROM entity_relations WHERE from_entity=?"
	case "incoming":
		q = "SELECT id,from_entity,to_entity,relation_type,weight,properties,created_at FROM entity_relations WHERE to_entity=?"
	default:
		q = "SELECT id,from_entity,to_entity,relation_type,weight,properties,created_at FROM entity_relations WHERE from_entity=? OR to_entity=?"
		rows, err := s.db.Query(q, entityID, entityID)
		if err != nil { return nil, err }
		defer rows.Close()
		return scanRelations(rows)
	}

	rows, err := s.db.Query(q, entityID)
	if err != nil { return nil, err }
	defer rows.Close()
	return scanRelations(rows)
}

func scanRelations(rows *sql.Rows) ([]EntityRelation, error) {
	var result []EntityRelation
	for rows.Next() {
		var r EntityRelation
		var propsJSON string
		rows.Scan(&r.ID, &r.FromEntity, &r.ToEntity, &r.RelationType, &r.Weight, &propsJSON, &r.CreatedAt)
		json.Unmarshal([]byte(propsJSON), &r.Properties)
		result = append(result, r)
	}
	return result, rows.Err()
}

func (s *Store) RemoveRelation(id string) error {
	_, err := s.db.Exec("DELETE FROM entity_relations WHERE id=?", id)
	return err
}

// --- Schema Inference ---

func (s *Store) SaveInference(si *SchemaInference) (*SchemaInference, error) {
	si.ID = newID("sinf_")
	now := time.Now().UTC().Format(time.RFC3339)
	si.CreatedAt = now
	si.ModifiedAt = now
	if si.FieldStats == nil { si.FieldStats = map[string]interface{}{} }
	if si.Metadata == nil { si.Metadata = map[string]interface{}{} }

	statsJSON, _ := json.Marshal(si.FieldStats)
	metaJSON, _ := json.Marshal(si.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO schema_inferences (id,source_type,source_id,inferred_schema,confidence,sample_count,field_stats,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		si.ID, si.SourceType, si.SourceID, si.InferredSchema, si.Confidence,
		si.SampleCount, string(statsJSON), string(metaJSON), now, now,
	)
	return si, err
}

func (s *Store) GetInference(sourceType, sourceID string) (*SchemaInference, error) {
	si := &SchemaInference{}
	var statsJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,source_type,source_id,inferred_schema,confidence,sample_count,field_stats,metadata,created_at,modified_at
		 FROM schema_inferences WHERE source_type=? AND source_id=? ORDER BY modified_at DESC LIMIT 1`,
		sourceType, sourceID,
	).Scan(&si.ID, &si.SourceType, &si.SourceID, &si.InferredSchema, &si.Confidence,
		&si.SampleCount, &statsJSON, &metaJSON, &si.CreatedAt, &si.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(statsJSON), &si.FieldStats)
	json.Unmarshal([]byte(metaJSON), &si.Metadata)
	return si, nil
}

func (s *Store) ListInferences(sourceType string, limit int) ([]SchemaInference, error) {
	if limit <= 0 { limit = 50 }
	q := "SELECT id,source_type,source_id,inferred_schema,confidence,sample_count,field_stats,metadata,created_at,modified_at FROM schema_inferences"
	var args []interface{}
	if sourceType != "" { q += " WHERE source_type=?"; args = append(args, sourceType) }
	q += fmt.Sprintf(" ORDER BY modified_at DESC LIMIT %d", limit)

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []SchemaInference
	for rows.Next() {
		var si SchemaInference
		var statsJSON, metaJSON string
		rows.Scan(&si.ID, &si.SourceType, &si.SourceID, &si.InferredSchema, &si.Confidence,
			&si.SampleCount, &statsJSON, &metaJSON, &si.CreatedAt, &si.ModifiedAt)
		json.Unmarshal([]byte(statsJSON), &si.FieldStats)
		json.Unmarshal([]byte(metaJSON), &si.Metadata)
		result = append(result, si)
	}
	return result, rows.Err()
}
