package agents

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
	`CREATE TABLE IF NOT EXISTS agent_messages (
		id           TEXT PRIMARY KEY,
		from_agent   TEXT NOT NULL,
		to_agent     TEXT NOT NULL,
		channel      TEXT DEFAULT 'direct',
		message_type TEXT DEFAULT 'text',
		subject      TEXT DEFAULT '',
		body         TEXT NOT NULL,
		in_reply_to  TEXT DEFAULT '',
		priority     INTEGER DEFAULT 0,
		status       TEXT DEFAULT 'sent',
		read_at      TEXT DEFAULT '',
		metadata     TEXT DEFAULT '{}',
		created_at   TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_amsg_to ON agent_messages(to_agent, status);
	CREATE INDEX IF NOT EXISTS idx_amsg_from ON agent_messages(from_agent);
	CREATE INDEX IF NOT EXISTS idx_amsg_channel ON agent_messages(channel);
	CREATE INDEX IF NOT EXISTS idx_amsg_reply ON agent_messages(in_reply_to) WHERE in_reply_to != '';

	CREATE TABLE IF NOT EXISTS agent_channels (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		channel_type TEXT DEFAULT 'topic',
		members     TEXT DEFAULT '[]',
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_achan_name ON agent_channels(name);

	CREATE TABLE IF NOT EXISTS coordination_protocols (
		id          TEXT PRIMARY KEY,
		name        TEXT NOT NULL,
		description TEXT DEFAULT '',
		protocol_type TEXT DEFAULT 'request_reply',
		steps       TEXT DEFAULT '[]',
		timeout_ms  INTEGER DEFAULT 30000,
		enabled     INTEGER DEFAULT 1,
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_cprot_name ON coordination_protocols(name);

	CREATE TABLE IF NOT EXISTS behavior_profiles (
		id          TEXT PRIMARY KEY,
		agent_id    TEXT NOT NULL,
		name        TEXT NOT NULL,
		traits      TEXT DEFAULT '{}',
		constraints TEXT DEFAULT '[]',
		goals       TEXT DEFAULT '[]',
		active      INTEGER DEFAULT 1,
		metadata    TEXT DEFAULT '{}',
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_bprof_agent ON behavior_profiles(agent_id, name);
	CREATE INDEX IF NOT EXISTS idx_bprof_active ON behavior_profiles(agent_id, active);`,
}

type Store struct{ db *sql.DB }

type Message struct {
	ID          string                 `json:"id"`
	FromAgent   string                 `json:"from_agent"`
	ToAgent     string                 `json:"to_agent"`
	Channel     string                 `json:"channel"`
	MessageType string                 `json:"message_type"`
	Subject     string                 `json:"subject"`
	Body        string                 `json:"body"`
	InReplyTo   string                 `json:"in_reply_to,omitempty"`
	Priority    int                    `json:"priority"`
	Status      string                 `json:"status"`
	ReadAt      string                 `json:"read_at,omitempty"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
}

type Channel struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	ChannelType string                 `json:"channel_type"`
	Members     []string               `json:"members"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
}

type Protocol struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	ProtocolType string                 `json:"protocol_type"`
	Steps        []interface{}          `json:"steps"`
	TimeoutMs    int64                  `json:"timeout_ms"`
	Enabled      bool                   `json:"enabled"`
	Metadata     map[string]interface{} `json:"metadata"`
	CreatedAt    string                 `json:"created_at"`
	ModifiedAt   string                 `json:"modified_at"`
}

type BehaviorProfile struct {
	ID          string                 `json:"id"`
	AgentID     string                 `json:"agent_id"`
	Name        string                 `json:"name"`
	Traits      map[string]interface{} `json:"traits"`
	Constraints []string               `json:"constraints"`
	Goals       []string               `json:"goals"`
	Active      bool                   `json:"active"`
	Metadata    map[string]interface{} `json:"metadata"`
	CreatedAt   string                 `json:"created_at"`
	ModifiedAt  string                 `json:"modified_at"`
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "agents", migrations); err != nil {
		return nil, fmt.Errorf("migrating agents schema: %w", err)
	}
	return &Store{db: coreDB}, nil
}

// --- Messages ---

func (s *Store) SendMessage(m *Message) (*Message, error) {
	m.ID = newID("amsg_")
	m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	if m.Status == "" { m.Status = "sent" }
	if m.Channel == "" { m.Channel = "direct" }
	if m.MessageType == "" { m.MessageType = "text" }
	if m.Metadata == nil { m.Metadata = map[string]interface{}{} }

	metaJSON, _ := json.Marshal(m.Metadata)
	_, err := s.db.Exec(
		`INSERT INTO agent_messages (id,from_agent,to_agent,channel,message_type,subject,body,in_reply_to,priority,status,metadata,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.FromAgent, m.ToAgent, m.Channel, m.MessageType,
		m.Subject, m.Body, m.InReplyTo, m.Priority, m.Status, string(metaJSON), m.CreatedAt,
	)
	return m, err
}

func (s *Store) GetMessage(id string) (*Message, error) {
	m := &Message{}
	var metaJSON string
	err := s.db.QueryRow(
		`SELECT id,from_agent,to_agent,channel,message_type,subject,body,in_reply_to,priority,status,COALESCE(read_at,''),metadata,created_at
		 FROM agent_messages WHERE id=?`, id,
	).Scan(&m.ID, &m.FromAgent, &m.ToAgent, &m.Channel, &m.MessageType,
		&m.Subject, &m.Body, &m.InReplyTo, &m.Priority, &m.Status, &m.ReadAt, &metaJSON, &m.CreatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(metaJSON), &m.Metadata)
	return m, nil
}

type MessageListOpts struct {
	ToAgent   string
	FromAgent string
	Channel   string
	Status    string
	Limit     int
}

func (s *Store) ListMessages(opts MessageListOpts) ([]Message, error) {
	q := `SELECT id,from_agent,to_agent,channel,message_type,subject,body,in_reply_to,priority,status,COALESCE(read_at,''),metadata,created_at
	      FROM agent_messages WHERE 1=1`
	var args []interface{}
	if opts.ToAgent != "" { q += " AND to_agent=?"; args = append(args, opts.ToAgent) }
	if opts.FromAgent != "" { q += " AND from_agent=?"; args = append(args, opts.FromAgent) }
	if opts.Channel != "" { q += " AND channel=?"; args = append(args, opts.Channel) }
	if opts.Status != "" { q += " AND status=?"; args = append(args, opts.Status) }
	q += " ORDER BY created_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var m Message
		var metaJSON string
		rows.Scan(&m.ID, &m.FromAgent, &m.ToAgent, &m.Channel, &m.MessageType,
			&m.Subject, &m.Body, &m.InReplyTo, &m.Priority, &m.Status, &m.ReadAt, &metaJSON, &m.CreatedAt)
		json.Unmarshal([]byte(metaJSON), &m.Metadata)
		result = append(result, m)
	}
	return result, rows.Err()
}

func (s *Store) MarkRead(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE agent_messages SET status='read', read_at=? WHERE id=?", now, id)
	return err
}

// Inbox returns unread messages for an agent.
func (s *Store) Inbox(agentID string, limit int) ([]Message, error) {
	return s.ListMessages(MessageListOpts{ToAgent: agentID, Status: "sent", Limit: limit})
}

// --- Channels ---

func (s *Store) CreateChannel(ch *Channel) (*Channel, error) {
	ch.ID = newID("chan_")
	now := time.Now().UTC().Format(time.RFC3339)
	ch.CreatedAt = now
	ch.ModifiedAt = now
	if ch.ChannelType == "" { ch.ChannelType = "topic" }
	if ch.Members == nil { ch.Members = []string{} }
	if ch.Metadata == nil { ch.Metadata = map[string]interface{}{} }

	membersJSON, _ := json.Marshal(ch.Members)
	metaJSON, _ := json.Marshal(ch.Metadata)
	_, err := s.db.Exec(
		`INSERT INTO agent_channels (id,name,description,channel_type,members,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		ch.ID, ch.Name, ch.Description, ch.ChannelType, string(membersJSON), string(metaJSON), now, now,
	)
	return ch, err
}

func (s *Store) GetChannel(id string) (*Channel, error) {
	ch := &Channel{}
	var membersJSON, metaJSON string
	err := s.db.QueryRow(
		`SELECT id,name,description,channel_type,members,metadata,created_at,modified_at
		 FROM agent_channels WHERE id=?`, id,
	).Scan(&ch.ID, &ch.Name, &ch.Description, &ch.ChannelType, &membersJSON, &metaJSON, &ch.CreatedAt, &ch.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(membersJSON), &ch.Members)
	json.Unmarshal([]byte(metaJSON), &ch.Metadata)
	return ch, nil
}

func (s *Store) ListChannels() ([]Channel, error) {
	rows, err := s.db.Query(
		"SELECT id,name,description,channel_type,members,metadata,created_at,modified_at FROM agent_channels ORDER BY name")
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Channel
	for rows.Next() {
		var ch Channel
		var membersJSON, metaJSON string
		rows.Scan(&ch.ID, &ch.Name, &ch.Description, &ch.ChannelType, &membersJSON, &metaJSON, &ch.CreatedAt, &ch.ModifiedAt)
		json.Unmarshal([]byte(membersJSON), &ch.Members)
		json.Unmarshal([]byte(metaJSON), &ch.Metadata)
		result = append(result, ch)
	}
	return result, rows.Err()
}

// --- Coordination Protocols ---

func (s *Store) CreateProtocol(p *Protocol) (*Protocol, error) {
	p.ID = newID("prot_")
	now := time.Now().UTC().Format(time.RFC3339)
	p.CreatedAt = now
	p.ModifiedAt = now
	if p.ProtocolType == "" { p.ProtocolType = "request_reply" }
	if p.TimeoutMs == 0 { p.TimeoutMs = 30000 }
	if p.Steps == nil { p.Steps = []interface{}{} }
	if p.Metadata == nil { p.Metadata = map[string]interface{}{} }

	stepsJSON, _ := json.Marshal(p.Steps)
	metaJSON, _ := json.Marshal(p.Metadata)
	_, err := s.db.Exec(
		`INSERT INTO coordination_protocols (id,name,description,protocol_type,steps,timeout_ms,enabled,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Description, p.ProtocolType,
		string(stepsJSON), p.TimeoutMs, boolToInt(p.Enabled), string(metaJSON), now, now,
	)
	return p, err
}

func (s *Store) ListProtocols() ([]Protocol, error) {
	rows, err := s.db.Query(
		`SELECT id,name,description,protocol_type,steps,timeout_ms,enabled,metadata,created_at,modified_at
		 FROM coordination_protocols ORDER BY name`)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Protocol
	for rows.Next() {
		var p Protocol
		var stepsJSON, metaJSON string
		var enabled int
		rows.Scan(&p.ID, &p.Name, &p.Description, &p.ProtocolType,
			&stepsJSON, &p.TimeoutMs, &enabled, &metaJSON, &p.CreatedAt, &p.ModifiedAt)
		p.Enabled = enabled != 0
		json.Unmarshal([]byte(stepsJSON), &p.Steps)
		json.Unmarshal([]byte(metaJSON), &p.Metadata)
		result = append(result, p)
	}
	return result, rows.Err()
}

// --- Behavior Profiles ---

func (s *Store) CreateProfile(bp *BehaviorProfile) (*BehaviorProfile, error) {
	bp.ID = newID("bprof_")
	now := time.Now().UTC().Format(time.RFC3339)
	bp.CreatedAt = now
	bp.ModifiedAt = now
	if bp.Traits == nil { bp.Traits = map[string]interface{}{} }
	if bp.Constraints == nil { bp.Constraints = []string{} }
	if bp.Goals == nil { bp.Goals = []string{} }
	if bp.Metadata == nil { bp.Metadata = map[string]interface{}{} }

	traitsJSON, _ := json.Marshal(bp.Traits)
	constraintsJSON, _ := json.Marshal(bp.Constraints)
	goalsJSON, _ := json.Marshal(bp.Goals)
	metaJSON, _ := json.Marshal(bp.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO behavior_profiles (id,agent_id,name,traits,constraints,goals,active,metadata,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		bp.ID, bp.AgentID, bp.Name, string(traitsJSON),
		string(constraintsJSON), string(goalsJSON), boolToInt(bp.Active), string(metaJSON), now, now,
	)
	return bp, err
}

func (s *Store) GetActiveProfile(agentID string) (*BehaviorProfile, error) {
	bp := &BehaviorProfile{}
	var traitsJSON, constraintsJSON, goalsJSON, metaJSON string
	var active int
	err := s.db.QueryRow(
		`SELECT id,agent_id,name,traits,constraints,goals,active,metadata,created_at,modified_at
		 FROM behavior_profiles WHERE agent_id=? AND active=1 ORDER BY modified_at DESC LIMIT 1`, agentID,
	).Scan(&bp.ID, &bp.AgentID, &bp.Name, &traitsJSON, &constraintsJSON, &goalsJSON,
		&active, &metaJSON, &bp.CreatedAt, &bp.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	bp.Active = active != 0
	json.Unmarshal([]byte(traitsJSON), &bp.Traits)
	json.Unmarshal([]byte(constraintsJSON), &bp.Constraints)
	json.Unmarshal([]byte(goalsJSON), &bp.Goals)
	json.Unmarshal([]byte(metaJSON), &bp.Metadata)
	return bp, nil
}

func (s *Store) ListProfiles(agentID string) ([]BehaviorProfile, error) {
	q := "SELECT id,agent_id,name,traits,constraints,goals,active,metadata,created_at,modified_at FROM behavior_profiles"
	var args []interface{}
	if agentID != "" { q += " WHERE agent_id=?"; args = append(args, agentID) }
	q += " ORDER BY modified_at DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []BehaviorProfile
	for rows.Next() {
		var bp BehaviorProfile
		var traitsJSON, constraintsJSON, goalsJSON, metaJSON string
		var active int
		rows.Scan(&bp.ID, &bp.AgentID, &bp.Name, &traitsJSON, &constraintsJSON, &goalsJSON,
			&active, &metaJSON, &bp.CreatedAt, &bp.ModifiedAt)
		bp.Active = active != 0
		json.Unmarshal([]byte(traitsJSON), &bp.Traits)
		json.Unmarshal([]byte(constraintsJSON), &bp.Constraints)
		json.Unmarshal([]byte(goalsJSON), &bp.Goals)
		json.Unmarshal([]byte(metaJSON), &bp.Metadata)
		result = append(result, bp)
	}
	return result, rows.Err()
}

func (s *Store) ActivateProfile(agentID, profileID string) error {
	s.db.Exec("UPDATE behavior_profiles SET active=0 WHERE agent_id=?", agentID)
	_, err := s.db.Exec("UPDATE behavior_profiles SET active=1, modified_at=? WHERE id=?",
		time.Now().UTC().Format(time.RFC3339), profileID)
	return err
}

func boolToInt(b bool) int {
	if b { return 1 }
	return 0
}
