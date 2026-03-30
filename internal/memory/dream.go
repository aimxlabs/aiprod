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
	Researched     int    `json:"researched"`
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

	// Phase 5: Quick web research to fill gaps
	researched, err := s.phaseResearch()
	if err != nil {
		fmt.Printf("[dream] Warning: research phase failed: %v\n", err)
	}
	result.Researched = researched

	// Phase 6: Generate behavioral guidance
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
	fmt.Printf("[dream] Dream cycle complete: expired=%d decayed=%d consolidated=%d reembedded=%d researched=%d reflections=%d total=%d\n",
		result.Expired, result.Decayed, result.Consolidated, result.Reembedded, result.Researched, result.Reflections, result.TotalMemories)

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

// --- Phase 5: Research ---
// Identify entities or topics in memory that could benefit from a quick web search.
// Only does lightweight lookups — not deep research.

const maxResearchQueries = 5

func (s *Store) phaseResearch() (int, error) {
	// Load all memories to find researchable gaps
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		WHERE namespace != '_system' AND namespace != 'inferred'
		ORDER BY importance DESC
		LIMIT 200`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var dump strings.Builder
	for rows.Next() {
		var ns, key, content string
		rows.Scan(&ns, &key, &content)
		dump.WriteString(fmt.Sprintf("- [%s] %s: %s\n", ns, key, content))
	}

	if dump.Len() == 0 {
		return 0, nil
	}

	// Ask LLM to identify what could be quickly looked up
	prompt := fmt.Sprintf(`You are reviewing an AI agent's memories. Identify up to %d things that could be quickly clarified with a simple web search.

Focus on:
- Named entities (companies, products, people) where we know the name but not what they do
- Technologies or tools mentioned without context
- Locations or organizations referenced without detail

Do NOT suggest searching for:
- Personal information about the user (privacy)
- Broad topics that would require deep research
- Things that are already well-described in memory

Current memories:
%s

For each suggestion, output a JSON line with: "query" (the search query) and "store_as" (a short key name for the result).
Output ONLY JSON lines. Output nothing if no searches are needed.`, maxResearchQueries, dump.String())

	resp, err := s.llm.Generate(
		"You are a research planner. Identify only quick, factual lookups. Output only JSON lines.",
		prompt, 0.1, 1024,
	)
	if err != nil {
		return 0, fmt.Errorf("research planning: %w", err)
	}

	type searchTask struct {
		Query   string `json:"query"`
		StoreAs string `json:"store_as"`
	}
	var tasks []searchTask
	for _, line := range strings.Split(resp.Response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var t searchTask
		if err := json.Unmarshal([]byte(line), &t); err == nil && t.Query != "" && t.StoreAs != "" {
			tasks = append(tasks, t)
		}
	}

	if len(tasks) > maxResearchQueries {
		tasks = tasks[:maxResearchQueries]
	}

	count := 0
	for _, task := range tasks {
		// Ask the local LLM what it knows about this topic
		answerResp, err := s.llm.Generate(
			"You are a factual knowledge base. Answer concisely in 1-2 sentences. If you don't know or aren't confident, respond with exactly SKIP.",
			fmt.Sprintf("What is %s? Give a brief factual description.", task.Query),
			0.1, 256,
		)
		if err != nil {
			continue
		}

		summary := strings.TrimSpace(answerResp.Response)
		if summary == "" || strings.HasPrefix(strings.ToUpper(summary), "SKIP") || strings.Contains(strings.ToLower(summary), "i don't know") || strings.Contains(strings.ToLower(summary), "i'm not sure") {
			continue
		}

		s.CreateMemory(&Memory{
			Namespace:  "researched",
			Key:        task.StoreAs,
			Content:    summary + " [from local LLM knowledge]",
			Importance: 0.5,
		})
		count++
		fmt.Printf("[dream] Researched: %s → %s\n", task.Query, task.StoreAs)
	}

	return count, nil
}

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

// --- Phase 5: Introspect ---
// Analyze existing memories to infer new knowledge, fill gaps, and derive
// insights — without ever needing to ask the user directly. The agent
// does its own homework.

func (s *Store) phaseReflect() (int, error) {
	// Load all memories grouped by namespace
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		WHERE namespace != '_system'
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

	count := 0

	// Step 1: Infer new knowledge from existing memories
	inferPrompt := fmt.Sprintf(`You are analyzing an AI agent's memory store. Your job is to find implicit knowledge that can be inferred from what's already stored, WITHOUT asking the user anything.

Look at the memories below and derive new facts by:
- Connecting dots between separate memories (e.g. if user asked about "restaurant payments" and also about "ReadyToPay", infer they might be in the restaurant tech industry)
- Identifying patterns in behavior (e.g. if most questions come during certain topics, infer interests)
- Extracting implicit preferences from how the user interacts (e.g. if they always ask for concise answers, infer they prefer brevity)
- Noting domain expertise signals (e.g. technical vocabulary suggests engineering background)

Current memories:
%s

For each inference, output a JSON line with: "namespace", "key", "content", "confidence" (0.0-1.0).
Only include inferences you're reasonably confident about (>0.5).
Do NOT suggest asking the user questions. Derive everything from existing data.
Output ONLY JSON lines, nothing else.`, dump.String())

	inferResp, err := s.llm.Generate(
		"You are an analytical system that derives implicit knowledge from explicit data. Output only JSON lines.",
		inferPrompt, 0.2, 4096,
	)
	if err != nil {
		fmt.Printf("[dream] Warning: inference failed: %v\n", err)
	} else {
		type inference struct {
			Namespace  string  `json:"namespace"`
			Key        string  `json:"key"`
			Content    string  `json:"content"`
			Confidence float64 `json:"confidence"`
		}
		for _, line := range strings.Split(inferResp.Response, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "{") {
				continue
			}
			var inf inference
			if err := json.Unmarshal([]byte(line), &inf); err == nil && inf.Key != "" && inf.Content != "" && inf.Confidence > 0.5 {
				ns := inf.Namespace
				if ns == "" {
					ns = "inferred"
				}
				s.CreateMemory(&Memory{
					Namespace:  ns,
					Key:        inf.Key,
					Content:    inf.Content + fmt.Sprintf(" [inferred, confidence: %.1f]", inf.Confidence),
					Importance: float64(inf.Confidence) * 0.6, // inferred memories start lower
				})
				count++
			}
		}
		if count > 0 {
			fmt.Printf("[dream] Inferred %d new memories from existing data\n", count)
		}
	}

	// Step 2: Generate behavioral guidance (not "ask about X" but "when X comes up, do Y")
	guidePrompt := fmt.Sprintf(`Based on this agent's memory store, generate behavioral guidance for future interactions.

Do NOT suggest asking the user questions or probing for information.
Instead, generate guidance like:
- "When discussing technology, prefer concrete examples over abstract explanations" (if user seems practical)
- "The user likely values brevity — keep responses concise unless asked to elaborate"
- "Show awareness of restaurant/payment industry context when relevant"

These should be natural behavioral adjustments, not interrogation strategies.

Current memories:
%s

Output 3-5 guidance statements as a plain text list, one per line starting with "- ".
Be specific and actionable. Base everything on evidence from the memories.`, dump.String())

	guideResp, err := s.llm.Generate(
		"You are a behavioral analyst producing guidance for an AI assistant. Be specific, never suggest asking the user questions.",
		guidePrompt, 0.3, 1024,
	)
	if err != nil {
		fmt.Printf("[dream] Warning: guidance generation failed: %v\n", err)
	} else {
		s.CreateMemory(&Memory{
			Namespace:  "_system",
			Key:        "dream-reflection",
			Content:    guideResp.Response,
			Importance: 0.9,
		})
		count++
		fmt.Printf("[dream] Behavioral guidance generated\n")
	}

	return count, nil
}
