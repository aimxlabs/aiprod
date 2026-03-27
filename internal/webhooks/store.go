package webhooks

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/garett/aiprod/internal/db"
	"github.com/gorilla/websocket"
)

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS webhook_subscriptions (
		id          TEXT PRIMARY KEY,
		channel_id  TEXT NOT NULL,
		server_url  TEXT NOT NULL,
		token       TEXT NOT NULL,
		label       TEXT DEFAULT '',
		active      INTEGER DEFAULT 1,
		created_at  TEXT NOT NULL,
		modified_at TEXT NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_wsub_channel ON webhook_subscriptions(channel_id);
	CREATE INDEX IF NOT EXISTS idx_wsub_active ON webhook_subscriptions(active);

	CREATE TABLE IF NOT EXISTS webhook_events (
		id           TEXT PRIMARY KEY,
		channel_id   TEXT NOT NULL,
		event_id     TEXT NOT NULL,
		headers      TEXT DEFAULT '{}',
		body         TEXT DEFAULT '',
		method       TEXT DEFAULT 'POST',
		source_ip    TEXT DEFAULT '',
		received_at  TEXT NOT NULL,
		delivered_at TEXT DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_wevt_channel ON webhook_events(channel_id, received_at);
	CREATE INDEX IF NOT EXISTS idx_wevt_remote ON webhook_events(event_id);`,
}

type Store struct {
	db    *sql.DB
	conns map[string]*listener // channelID → active WS listener
	mu    sync.Mutex
}

// Subscription represents a hookd channel subscription.
type Subscription struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channel_id"`
	ServerURL  string `json:"server_url"`
	Token      string `json:"token"`
	Label      string `json:"label,omitempty"`
	Active     bool   `json:"active"`
	CreatedAt  string `json:"created_at"`
	ModifiedAt string `json:"modified_at"`
}

// Event is a webhook event received from hookd.
type Event struct {
	ID         string            `json:"id"`
	ChannelID  string            `json:"channel_id"`
	EventID    string            `json:"event_id"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Method     string            `json:"method"`
	SourceIP   string            `json:"source_ip,omitempty"`
	ReceivedAt string            `json:"received_at"`
	DeliveredAt string           `json:"delivered_at,omitempty"`
}

// listener tracks a WebSocket connection to a hookd server.
type listener struct {
	sub    Subscription
	cancel chan struct{}
}

func newID(prefix string) string {
	b := make([]byte, 12)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func NewStore(coreDB *sql.DB) (*Store, error) {
	if err := db.Migrate(coreDB, "webhooks", migrations); err != nil {
		return nil, fmt.Errorf("migrating webhooks schema: %w", err)
	}
	return &Store{db: coreDB, conns: make(map[string]*listener)}, nil
}

// --- Subscriptions ---

func (s *Store) CreateSubscription(sub *Subscription) (*Subscription, error) {
	sub.ID = newID("wsub_")
	now := time.Now().UTC().Format(time.RFC3339)
	sub.CreatedAt = now
	sub.ModifiedAt = now
	sub.Active = true

	_, err := s.db.Exec(
		`INSERT INTO webhook_subscriptions (id,channel_id,server_url,token,label,active,created_at,modified_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		sub.ID, sub.ChannelID, sub.ServerURL, sub.Token, sub.Label, 1, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating subscription: %w", err)
	}
	return sub, nil
}

func (s *Store) GetSubscription(id string) (*Subscription, error) {
	sub := &Subscription{}
	var active int
	err := s.db.QueryRow(
		`SELECT id,channel_id,server_url,token,label,active,created_at,modified_at
		 FROM webhook_subscriptions WHERE id=?`, id,
	).Scan(&sub.ID, &sub.ChannelID, &sub.ServerURL, &sub.Token, &sub.Label,
		&active, &sub.CreatedAt, &sub.ModifiedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	sub.Active = active != 0
	return sub, nil
}

func (s *Store) ListSubscriptions() ([]Subscription, error) {
	rows, err := s.db.Query(
		`SELECT id,channel_id,server_url,token,label,active,created_at,modified_at
		 FROM webhook_subscriptions ORDER BY created_at DESC`)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Subscription
	for rows.Next() {
		var sub Subscription
		var active int
		rows.Scan(&sub.ID, &sub.ChannelID, &sub.ServerURL, &sub.Token, &sub.Label,
			&active, &sub.CreatedAt, &sub.ModifiedAt)
		sub.Active = active != 0
		result = append(result, sub)
	}
	return result, rows.Err()
}

func (s *Store) DeleteSubscription(id string) error {
	s.StopListener(id)
	_, err := s.db.Exec("DELETE FROM webhook_subscriptions WHERE id=?", id)
	return err
}

func (s *Store) SetActive(id string, active bool) error {
	v := 0
	if active { v = 1 }
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec("UPDATE webhook_subscriptions SET active=?, modified_at=? WHERE id=?", v, now, id)
	if err != nil { return err }
	if !active { s.StopListener(id) }
	return nil
}

// --- Events ---

func (s *Store) storeEvent(channelID, eventID string, headers map[string]string, body, method, ip, receivedAt string) (*Event, error) {
	id := newID("wevt_")
	now := time.Now().UTC().Format(time.RFC3339)
	if receivedAt == "" { receivedAt = now }
	if headers == nil { headers = map[string]string{} }

	headersJSON, _ := json.Marshal(headers)
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO webhook_events (id,channel_id,event_id,headers,body,method,source_ip,received_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		id, channelID, eventID, string(headersJSON), body, method, ip, receivedAt,
	)
	if err != nil { return nil, err }

	return &Event{
		ID: id, ChannelID: channelID, EventID: eventID,
		Headers: headers, Body: body, Method: method, SourceIP: ip,
		ReceivedAt: receivedAt,
	}, nil
}

type EventListOpts struct {
	ChannelID string
	Limit     int
}

func (s *Store) ListEvents(opts EventListOpts) ([]Event, error) {
	q := `SELECT id,channel_id,event_id,headers,body,method,source_ip,received_at,COALESCE(delivered_at,'')
	      FROM webhook_events WHERE 1=1`
	var args []interface{}
	if opts.ChannelID != "" { q += " AND channel_id=?"; args = append(args, opts.ChannelID) }
	q += " ORDER BY received_at DESC"
	if opts.Limit > 0 { q += fmt.Sprintf(" LIMIT %d", opts.Limit) } else { q += " LIMIT 50" }

	rows, err := s.db.Query(q, args...)
	if err != nil { return nil, err }
	defer rows.Close()
	var result []Event
	for rows.Next() {
		var evt Event
		var headersJSON string
		rows.Scan(&evt.ID, &evt.ChannelID, &evt.EventID, &headersJSON, &evt.Body,
			&evt.Method, &evt.SourceIP, &evt.ReceivedAt, &evt.DeliveredAt)
		json.Unmarshal([]byte(headersJSON), &evt.Headers)
		if evt.Headers == nil { evt.Headers = map[string]string{} }
		result = append(result, evt)
	}
	return result, rows.Err()
}

func (s *Store) GetEvent(id string) (*Event, error) {
	evt := &Event{}
	var headersJSON string
	err := s.db.QueryRow(
		`SELECT id,channel_id,event_id,headers,body,method,source_ip,received_at,COALESCE(delivered_at,'')
		 FROM webhook_events WHERE id=?`, id,
	).Scan(&evt.ID, &evt.ChannelID, &evt.EventID, &headersJSON, &evt.Body,
		&evt.Method, &evt.SourceIP, &evt.ReceivedAt, &evt.DeliveredAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	json.Unmarshal([]byte(headersJSON), &evt.Headers)
	if evt.Headers == nil { evt.Headers = map[string]string{} }
	return evt, nil
}

// --- WebSocket Listener ---

// StartListener connects to a hookd server via WebSocket and stores incoming events.
func (s *Store) StartListener(subID string) error {
	sub, err := s.GetSubscription(subID)
	if err != nil { return err }
	if sub == nil { return fmt.Errorf("subscription not found: %s", subID) }
	if !sub.Active { return fmt.Errorf("subscription %s is not active", subID) }

	s.mu.Lock()
	if _, exists := s.conns[subID]; exists {
		s.mu.Unlock()
		return fmt.Errorf("listener already running for %s", subID)
	}
	l := &listener{sub: *sub, cancel: make(chan struct{})}
	s.conns[subID] = l
	s.mu.Unlock()

	go s.runListener(l)
	return nil
}

// StopListener disconnects an active WebSocket listener.
func (s *Store) StopListener(subID string) {
	s.mu.Lock()
	l, exists := s.conns[subID]
	if exists {
		close(l.cancel)
		delete(s.conns, subID)
	}
	s.mu.Unlock()
}

// ListenerStatus returns which subscriptions have active listeners.
func (s *Store) ListenerStatus() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := make(map[string]bool)
	for id := range s.conns {
		status[id] = true
	}
	return status
}

func (s *Store) runListener(l *listener) {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-l.cancel:
			return
		default:
		}

		err := s.connectAndListen(l)
		if err != nil {
			log.Printf("webhooks: listener %s disconnected: %v", l.sub.ID, err)
		}

		select {
		case <-l.cancel:
			return
		case <-time.After(backoff):
		}
		backoff = backoff * 2
		if backoff > maxBackoff { backoff = maxBackoff }
	}
}

func (s *Store) connectAndListen(l *listener) error {
	wsURL, err := buildWSURL(l.sub.ServerURL)
	if err != nil { return err }

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil { return fmt.Errorf("dial: %w", err) }
	defer conn.Close()

	// Auth
	auth := map[string]string{"type": "auth", "token": l.sub.Token}
	if err := conn.WriteJSON(auth); err != nil { return fmt.Errorf("auth send: %w", err) }

	// Read auth response
	var resp map[string]interface{}
	if err := conn.ReadJSON(&resp); err != nil { return fmt.Errorf("auth read: %w", err) }
	if resp["type"] == "auth_error" {
		return fmt.Errorf("auth failed: %v", resp["message"])
	}

	// Subscribe
	sub := map[string]string{"type": "subscribe", "channelId": l.sub.ChannelID}
	if err := conn.WriteJSON(sub); err != nil { return fmt.Errorf("subscribe send: %w", err) }

	// Read subscribe confirmation
	if err := conn.ReadJSON(&resp); err != nil { return fmt.Errorf("subscribe read: %w", err) }

	log.Printf("webhooks: listening on %s (channel %s)", l.sub.ID, l.sub.ChannelID)

	// Event loop
	for {
		select {
		case <-l.cancel:
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		var msg map[string]interface{}
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("read: %w", err)
		}

		msgType, _ := msg["type"].(string)
		switch msgType {
		case "event":
			eventID, _ := msg["eventId"].(string)
			channelID, _ := msg["channelId"].(string)
			body, _ := msg["body"].(string)
			method, _ := msg["method"].(string)
			ip, _ := msg["ip"].(string)
			receivedAt, _ := msg["receivedAt"].(string)

			headers := map[string]string{}
			if h, ok := msg["headers"].(map[string]interface{}); ok {
				for k, v := range h {
					if sv, ok := v.(string); ok { headers[k] = sv }
				}
			}

			s.storeEvent(channelID, eventID, headers, body, method, ip, receivedAt)

			// ACK
			ack := map[string]string{"type": "ack", "eventId": eventID}
			conn.WriteJSON(ack)

		case "pong":
			// keep-alive response, nothing to do

		case "error":
			log.Printf("webhooks: server error: %v", msg["message"])
		}
	}
}

// --- Polling ---

// Poll fetches undelivered events from a hookd server via HTTP polling.
func (s *Store) Poll(subID string) ([]Event, error) {
	sub, err := s.GetSubscription(subID)
	if err != nil { return nil, err }
	if sub == nil { return nil, fmt.Errorf("subscription not found: %s", subID) }

	pollURL := strings.TrimRight(sub.ServerURL, "/") + "/api/channels/" + sub.ChannelID + "/poll"
	req, err := http.NewRequest("GET", pollURL, nil)
	if err != nil { return nil, err }
	req.Header.Set("Authorization", "Bearer "+sub.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil { return nil, fmt.Errorf("poll request: %w", err) }
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("poll returned %d: %s", resp.StatusCode, string(body))
	}

	var pollResp struct {
		Events []struct {
			ID         string            `json:"id"`
			ChannelID  string            `json:"channelId"`
			Headers    map[string]string `json:"headers"`
			Body       string            `json:"body"`
			Method     string            `json:"method"`
			SourceIP   string            `json:"sourceIp"`
			ReceivedAt int64             `json:"receivedAt"`
		} `json:"events"`
		Cursor string `json:"cursor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pollResp); err != nil {
		return nil, fmt.Errorf("poll decode: %w", err)
	}

	var stored []Event
	var ackIDs []string
	for _, e := range pollResp.Events {
		receivedAt := time.Unix(e.ReceivedAt, 0).UTC().Format(time.RFC3339)
		evt, err := s.storeEvent(sub.ChannelID, e.ID, e.Headers, e.Body, e.Method, e.SourceIP, receivedAt)
		if err != nil { continue }
		stored = append(stored, *evt)
		ackIDs = append(ackIDs, e.ID)
	}

	// ACK polled events
	if len(ackIDs) > 0 {
		s.ackPolled(sub, ackIDs)
	}

	return stored, nil
}

func (s *Store) ackPolled(sub *Subscription, eventIDs []string) {
	ackURL := strings.TrimRight(sub.ServerURL, "/") + "/api/channels/" + sub.ChannelID + "/ack"
	body, _ := json.Marshal(map[string]interface{}{"eventIds": eventIDs})
	req, _ := http.NewRequest("POST", ackURL, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+sub.Token)
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)
}

// --- Helpers ---

func buildWSURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil { return "", err }
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	u.Path = "/ws"
	return u.String(), nil
}

// StartAllListeners starts WebSocket listeners for all active subscriptions.
func (s *Store) StartAllListeners() {
	subs, err := s.ListSubscriptions()
	if err != nil { return }
	for _, sub := range subs {
		if sub.Active {
			s.StartListener(sub.ID)
		}
	}
}

// StopAllListeners stops all active WebSocket listeners.
func (s *Store) StopAllListeners() {
	s.mu.Lock()
	for id, l := range s.conns {
		close(l.cancel)
		delete(s.conns, id)
	}
	s.mu.Unlock()
}
