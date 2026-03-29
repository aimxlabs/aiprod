package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DreamResult captures what happened during a dream cycle.
type DreamResult struct {
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	Expired        int    `json:"expired"`
	Decayed        int    `json:"decayed"`
	Consolidated   int    `json:"consolidated"`
	Reembedded     int    `json:"reembedded"`
	Reflections    int    `json:"reflections"`
	TotalMemories  int    `json:"total_memories"`
}

// Dream runs a full maintenance cycle on the memory store.
// It consolidates, decays, prunes, re-embeds, and reflects.
// Designed to run nightly via cron or on-demand via API.
func (s *Store) Dream() (*DreamResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("dream requires LLM client (Ollama) — set AIPROD_OLLAMA_URL")
	}

	result := &DreamResult{
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	fmt.Println("[dream] Starting dream cycle...")

	// Phase 1: Clean up expired memories
	expired, err := s.phaseExpire()
	if err != nil {
		fmt.Printf("[dream] Warning: expire phase failed: %v\n", err)
	}
	result.Expired = expired

	// Phase 2: Decay old, unused memories
	decayed, err := s.phaseDecay()
	if err != nil {
		fmt.Printf("[dream] Warning: decay phase failed: %v\n", err)
	}
	result.Decayed = decayed

	// Phase 3: Consolidate related memories per namespace
	consolidated, err := s.phaseConsolidate()
	if err != nil {
		fmt.Printf("[dream] Warning: consolidation phase failed: %v\n", err)
	}
	result.Consolidated = consolidated

	// Phase 4: Re-embed memories missing embeddings
	reembedded, err := s.phaseReembed()
	if err != nil {
		fmt.Printf("[dream] Warning: re-embed phase failed: %v\n", err)
	}
	result.Reembedded = reembedded

	// Phase 5: Generate a reflection on what the agent knows
	reflections, err := s.phaseReflect()
	if err != nil {
		fmt.Printf("[dream] Warning: reflection phase failed: %v\n", err)
	}
	result.Reflections = reflections

	// Rebuild vector index after all changes
	if s.vecIndex != nil {
		s.rebuildVecIndex()
	}

	// Count total memories
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&count)
	result.TotalMemories = count

	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	fmt.Printf("[dream] Dream cycle complete: expired=%d decayed=%d consolidated=%d reembedded=%d reflections=%d total=%d\n",
		result.Expired, result.Decayed, result.Consolidated, result.Reembedded, result.Reflections, result.TotalMemories)

	return result, nil
}

// --- Phase 1: Expire ---

func (s *Store) phaseExpire() (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		"DELETE FROM memories WHERE expires_at != '' AND expires_at <= ?", now,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		fmt.Printf("[dream] Expired %d memories\n", n)
	}
	return int(n), nil
}

// --- Phase 2: Decay ---
// Reduce importance of memories that haven't been accessed in a long time.
// Memories below a threshold after decay get pruned.

const (
	decayAgeDays       = 30  // start decaying after 30 days without access
	decayRate          = 0.1 // reduce importance by 10% per cycle
	pruneThreshold     = 0.05 // remove memories with importance below this
)

func (s *Store) phaseDecay() (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -decayAgeDays).Format(time.RFC3339)

	// Decay importance of stale memories
	_, err := s.db.Exec(`
		UPDATE memories SET
			importance = importance * (1.0 - ?),
			modified_at = ?
		WHERE (last_accessed_at < ? OR last_accessed_at = '')
		  AND modified_at < ?
		  AND importance > ?`,
		decayRate,
		time.Now().UTC().Format(time.RFC3339),
		cutoff, cutoff,
		pruneThreshold,
	)
	if err != nil {
		return 0, err
	}

	// Prune memories that decayed below threshold
	res, err := s.db.Exec(
		"DELETE FROM memories WHERE importance <= ? AND importance > 0", pruneThreshold,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		fmt.Printf("[dream] Pruned %d decayed memories\n", n)
	}
	return int(n), nil
}

// --- Phase 3: Consolidate ---
// For each namespace with many memories, use the LLM to merge related ones.

const consolidateThreshold = 10 // consolidate when namespace has more than this

func (s *Store) phaseConsolidate() (int, error) {
	// Find namespaces with many memories
	rows, err := s.db.Query(`
		SELECT namespace, COUNT(*) as cnt
		FROM memories
		GROUP BY namespace
		HAVING cnt > ?`, consolidateThreshold,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var namespaces []string
	for rows.Next() {
		var ns string
		var cnt int
		rows.Scan(&ns, &cnt)
		namespaces = append(namespaces, ns)
	}

	total := 0
	for _, ns := range namespaces {
		merged, err := s.consolidateNamespace(ns)
		if err != nil {
			fmt.Printf("[dream] Warning: consolidation of %s failed: %v\n", ns, err)
			continue
		}
		total += merged
	}
	return total, nil
}

func (s *Store) consolidateNamespace(namespace string) (int, error) {
	// Load all memories in this namespace
	memories, err := s.ListMemories(MemoryListOpts{
		Namespace: namespace,
		Limit:     100,
	})
	if err != nil {
		return 0, err
	}
	if len(memories) <= consolidateThreshold {
		return 0, nil
	}

	// Build a summary of all memories for the LLM
	var memDump strings.Builder
	for _, m := range memories {
		memDump.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Namespace, m.Key, m.Content))
	}

	prompt := fmt.Sprintf(`You are a memory consolidation system. Below are %d memories in the "%s" namespace.
Many may be redundant, outdated, or could be merged into fewer, richer entries.

Current memories:
%s

Consolidate these into the minimum number of distinct, non-redundant memories.
For each consolidated memory, output a JSON object on its own line with fields: "key" and "content".
Keep the most recent and accurate information. Drop duplicates and outdated entries.
Output ONLY the JSON lines, nothing else.`, len(memories), namespace, memDump.String())

	resp, err := s.llm.Generate(
		"You are a precise memory consolidation engine. Output only JSON lines.",
		prompt, 0.1, 4096,
	)
	if err != nil {
		return 0, fmt.Errorf("LLM consolidation: %w", err)
	}

	// Parse consolidated memories from LLM response
	type consolidatedMem struct {
		Key     string `json:"key"`
		Content string `json:"content"`
	}
	var consolidated []consolidatedMem
	for _, line := range strings.Split(resp.Response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var cm consolidatedMem
		if err := json.Unmarshal([]byte(line), &cm); err == nil && cm.Key != "" && cm.Content != "" {
			consolidated = append(consolidated, cm)
		}
	}

	if len(consolidated) == 0 || len(consolidated) >= len(memories) {
		// LLM didn't reduce anything or failed to parse — skip
		return 0, nil
	}

	fmt.Printf("[dream] Consolidating %s: %d memories → %d\n", namespace, len(memories), len(consolidated))

	// Delete old memories in this namespace
	for _, m := range memories {
		s.DeleteMemory(m.ID)
	}

	// Create consolidated memories
	for _, cm := range consolidated {
		s.CreateMemory(&Memory{
			AgentID:   memories[0].AgentID,
			Namespace: namespace,
			Key:       cm.Key,
			Content:   cm.Content,
			Importance: 0.7, // consolidated memories get a boost
		})
	}

	return len(memories) - len(consolidated), nil
}

// --- Phase 4: Re-embed ---
// Generate embeddings for memories that don't have them yet.

func (s *Store) phaseReembed() (int, error) {
	rows, err := s.db.Query(
		"SELECT id, key, content FROM memories WHERE embedding = '' OR embedding IS NULL",
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, key, content string
		rows.Scan(&id, &key, &content)

		embText := key + ": " + content
		vec, err := s.llm.Embed(embText)
		if err != nil {
			continue
		}
		embJSON, err := json.Marshal(vec)
		if err != nil {
			continue
		}
		s.db.Exec("UPDATE memories SET embedding = ? WHERE id = ?", string(embJSON), id)
		count++
	}

	if count > 0 {
		fmt.Printf("[dream] Re-embedded %d memories\n", count)
	}
	return count, nil
}

// --- Phase 5: Reflect ---
// Generate a high-level reflection on what the agent knows,
// stored as a special memory for quick context loading.

func (s *Store) phaseReflect() (int, error) {
	// Load all memories grouped by namespace
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		ORDER BY namespace, importance DESC
		LIMIT 200`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var dump strings.Builder
	currentNS := ""
	for rows.Next() {
		var ns, key, content string
		rows.Scan(&ns, &key, &content)
		if ns != currentNS {
			dump.WriteString(fmt.Sprintf("\n## %s\n", ns))
			currentNS = ns
		}
		dump.WriteString(fmt.Sprintf("- %s: %s\n", key, content))
	}

	if dump.Len() == 0 {
		return 0, nil
	}

	prompt := fmt.Sprintf(`You are reviewing an AI agent's complete memory store.
Produce a brief reflection (3-5 sentences) summarizing:
1. What the agent knows about its user(s)
2. Key domain knowledge it has accumulated
3. Any gaps or areas where knowledge seems thin
4. Suggestions for what to learn next

Memory contents:
%s

Write the reflection as plain text, no JSON.`, dump.String())

	resp, err := s.llm.Generate(
		"You are a reflective AI system analyzing your own knowledge.",
		prompt, 0.3, 1024,
	)
	if err != nil {
		return 0, fmt.Errorf("LLM reflection: %w", err)
	}

	// Store reflection as a special memory
	s.CreateMemory(&Memory{
		Namespace:  "_system",
		Key:        "dream-reflection",
		Content:    resp.Response,
		Importance: 0.9,
	})

	fmt.Printf("[dream] Reflection generated\n")
	return 1, nil
}
