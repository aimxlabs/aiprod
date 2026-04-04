package notify

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	checkInterval = 5 * time.Minute
	// How far ahead to warn about upcoming due dates
	dueSoonWindow = 2 * time.Hour
)

// Runner checks for actionable items and delivers notifications.
type Runner struct {
	db     *sql.DB
	stop   chan struct{}
	client *http.Client
}

func New(db *sql.DB) *Runner {
	return &Runner{
		db:     db,
		stop:   make(chan struct{}),
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Start begins the check loop. Call Stop() to shut down.
func (r *Runner) Start() {
	go r.loop()
}

func (r *Runner) Stop() {
	close(r.stop)
}

func (r *Runner) loop() {
	// Run first check shortly after startup (30s) so we don't miss things
	timer := time.NewTimer(30 * time.Second)
	for {
		select {
		case <-r.stop:
			timer.Stop()
			return
		case <-timer.C:
			r.checkAll()
			timer.Reset(checkInterval)
		}
	}
}

func (r *Runner) checkAll() {
	agentIDs, err := r.distinctAgentIDs()
	if err != nil {
		fmt.Printf("[notify] Error listing agents: %v\n", err)
		return
	}
	for _, agentID := range agentIDs {
		r.checkAgent(agentID)
	}
}

func (r *Runner) checkAgent(agentID string) {
	cfg := r.getNotifyConfig(agentID)
	if cfg.telegramToken == "" || cfg.telegramChatID == "" {
		return // no notification channel configured
	}

	var messages []string

	// 1. Check for overdue tasks
	overdue := r.overdueOrDueSoon(agentID)
	messages = append(messages, overdue...)

	// 2. Check for pending notifications (written by tools, dream phases, etc.)
	pending := r.consumePendingNotifications(agentID)
	messages = append(messages, pending...)

	// 3. Check for memories expiring soon (user-set reminders)
	expiring := r.expiringMemories(agentID)
	messages = append(messages, expiring...)

	if len(messages) == 0 {
		return
	}

	body := strings.Join(messages, "\n\n")
	if err := r.sendTelegram(cfg.telegramToken, cfg.telegramChatID, body); err != nil {
		fmt.Printf("[notify] Failed to send Telegram to %s: %v\n", agentID, err)
	} else {
		fmt.Printf("[notify] Sent %d notification(s) to %s\n", len(messages), agentID)
	}
}

type notifyConfig struct {
	telegramToken  string
	telegramChatID string
}

func (r *Runner) getNotifyConfig(agentID string) notifyConfig {
	var cfg notifyConfig
	rows, err := r.db.Query(
		`SELECT key, content FROM memories WHERE agent_id = ? AND namespace = '_system' AND key IN ('notify-telegram-token', 'notify-telegram-chat-id')`,
		agentID,
	)
	if err != nil {
		return cfg
	}
	defer rows.Close()
	for rows.Next() {
		var key, content string
		rows.Scan(&key, &content)
		switch key {
		case "notify-telegram-token":
			cfg.telegramToken = strings.TrimSpace(content)
		case "notify-telegram-chat-id":
			cfg.telegramChatID = strings.TrimSpace(content)
		}
	}
	return cfg
}

// overdueOrDueSoon finds tasks that are overdue or due within the next window.
func (r *Runner) overdueOrDueSoon(agentID string) []string {
	now := time.Now().UTC()
	cutoff := now.Add(dueSoonWindow).Format(time.RFC3339)

	rows, err := r.db.Query(`
		SELECT title, status, due_date FROM tasks
		WHERE agent_id = ?
		  AND due_date != ''
		  AND due_date <= ?
		  AND status NOT IN ('done', 'cancelled')
		ORDER BY due_date ASC
		LIMIT 10`,
		agentID, cutoff,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []string
	for rows.Next() {
		var title, status, dueDate string
		rows.Scan(&title, &status, &dueDate)

		due, err := time.Parse(time.RFC3339, dueDate)
		if err != nil {
			continue
		}

		// Skip if we already notified about this task recently
		if r.wasNotifiedRecently(agentID, "task-due:"+title) {
			continue
		}

		var urgency string
		if due.Before(now) {
			urgency = "OVERDUE"
		} else {
			urgency = fmt.Sprintf("due in %s", time.Until(due).Round(time.Minute))
		}
		msgs = append(msgs, fmt.Sprintf("📋 %s: \"%s\" [%s]", urgency, title, status))
		r.markNotified(agentID, "task-due:"+title)
	}
	return msgs
}

// consumePendingNotifications reads and deletes _system/pending-notification-* memories
// that are ready to deliver. Notifications with expires_at in the future are skipped (scheduled).
// Notifications with empty expires_at or expires_at <= now are delivered immediately.
func (r *Runner) consumePendingNotifications(agentID string) []string {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := r.db.Query(`
		SELECT id, content FROM memories
		WHERE agent_id = ? AND namespace = '_system' AND key LIKE 'pending-notification-%'
		  AND (expires_at = '' OR expires_at IS NULL OR expires_at <= ?)
		ORDER BY created_at ASC
		LIMIT 20`,
		agentID, now,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []string
	var ids []string
	for rows.Next() {
		var id, content string
		rows.Scan(&id, &content)
		msgs = append(msgs, content)
		ids = append(ids, id)
	}

	// Delete consumed notifications
	for _, id := range ids {
		r.db.Exec("DELETE FROM memories WHERE id = ?", id)
	}
	return msgs
}

// expiringMemories finds memories with expires_at in the near future.
func (r *Runner) expiringMemories(agentID string) []string {
	now := time.Now().UTC()
	// Look for memories expiring in the next check interval + a buffer
	cutoff := now.Add(checkInterval + 1*time.Minute).Format(time.RFC3339)

	rows, err := r.db.Query(`
		SELECT key, content, expires_at FROM memories
		WHERE agent_id = ?
		  AND expires_at != ''
		  AND expires_at > ?
		  AND expires_at <= ?
		  AND namespace != '_system'
		ORDER BY expires_at ASC
		LIMIT 10`,
		agentID, now.Format(time.RFC3339), cutoff,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []string
	for rows.Next() {
		var key, content, expiresAt string
		rows.Scan(&key, &content, &expiresAt)

		if r.wasNotifiedRecently(agentID, "expiry:"+key) {
			continue
		}

		msgs = append(msgs, fmt.Sprintf("⏰ Reminder: %s", content))
		r.markNotified(agentID, "expiry:"+key)
	}
	return msgs
}

// wasNotifiedRecently checks if we already sent a notification for this key in the last check interval.
// Uses a lightweight _system memory as a dedup marker.
func (r *Runner) wasNotifiedRecently(agentID, dedupKey string) bool {
	var modifiedAt string
	err := r.db.QueryRow(
		`SELECT modified_at FROM memories WHERE agent_id = ? AND namespace = '_notify' AND key = ?`,
		agentID, dedupKey,
	).Scan(&modifiedAt)
	if err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, modifiedAt)
	if err != nil {
		return false
	}
	// Don't re-notify for 24 hours
	return time.Since(t) < 24*time.Hour
}

func (r *Runner) markNotified(agentID, dedupKey string) {
	now := time.Now().UTC().Format(time.RFC3339)
	// Upsert — try update first, then insert
	res, _ := r.db.Exec(
		`UPDATE memories SET modified_at = ? WHERE agent_id = ? AND namespace = '_notify' AND key = ?`,
		now, agentID, dedupKey,
	)
	if n, _ := res.RowsAffected(); n == 0 {
		r.db.Exec(
			`INSERT INTO memories (id, agent_id, namespace, key, content, importance, created_at, modified_at)
			 VALUES (?, ?, '_notify', ?, 'notified', 0, ?, ?)`,
			fmt.Sprintf("ntfy_%s_%d", dedupKey, time.Now().UnixNano()), agentID, dedupKey, now, now,
		)
	}
}

func (r *Runner) sendTelegram(token, chatID, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload, _ := json.Marshal(map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "Markdown",
	})
	resp, err := r.client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		// Retry without Markdown if parse fails
		if resp.StatusCode == 400 && strings.Contains(string(body), "parse") {
			payload, _ = json.Marshal(map[string]interface{}{
				"chat_id": chatID,
				"text":    text,
			})
			resp2, err := r.client.Post(url, "application/json", bytes.NewReader(payload))
			if err != nil {
				return err
			}
			defer resp2.Body.Close()
			if resp2.StatusCode != 200 {
				return fmt.Errorf("telegram API error: %d", resp2.StatusCode)
			}
			return nil
		}
		return fmt.Errorf("telegram API error: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

func (r *Runner) distinctAgentIDs() ([]string, error) {
	rows, err := r.db.Query("SELECT DISTINCT agent_id FROM memories WHERE agent_id != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
