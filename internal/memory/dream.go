package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FactWriter is satisfied by knowledge.Store — kept as an interface to avoid circular imports.
type FactWriter interface {
	CreateFactRaw(agentID, subject, predicate, object string, confidence float64) error
}

// DreamResult captures what happened during a dream cycle.
type DreamResult struct {
	AgentID          string `json:"agent_id"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at"`
	Expired          int    `json:"expired"`
	Decayed          int    `json:"decayed"`
	Consolidated     int    `json:"consolidated"`
	Reembedded       int    `json:"reembedded"`
	Researched       int    `json:"researched"`
	Reflections      int    `json:"reflections"`
	Reviewed         int    `json:"reviewed"`
	KnowledgeExtracted int  `json:"knowledge_extracted"`
	TotalMemories    int    `json:"total_memories"`
}

// Dream runs a full maintenance cycle on the memory store.
// It consolidates, decays, prunes, re-embeds, and reflects.
// Designed to run nightly via cron or on-demand via API.
func (s *Store) Dream(agentID string) (*DreamResult, error) {
	if s.llm == nil {
		return nil, fmt.Errorf("dream requires LLM client (Ollama) — set AIPROD_OLLAMA_URL")
	}

	result := &DreamResult{
		AgentID:   agentID,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	fmt.Printf("[dream] Starting dream cycle for agent %s...\n", agentID)

	expired, err := s.phaseExpire(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: expire phase failed: %v\n", err)
	}
	result.Expired = expired

	decayed, err := s.phaseDecay(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: decay phase failed: %v\n", err)
	}
	result.Decayed = decayed

	consolidated, err := s.phaseConsolidate(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: consolidation phase failed: %v\n", err)
	}
	result.Consolidated = consolidated

	reembedded, err := s.phaseReembed(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: re-embed phase failed: %v\n", err)
	}
	result.Reembedded = reembedded

	researched, err := s.phaseResearch(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: research phase failed: %v\n", err)
	}
	result.Researched = researched

	reflections, err := s.phaseReflect(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: reflection phase failed: %v\n", err)
	}
	result.Reflections = reflections

	extracted, err := s.phaseExtractKnowledge(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: knowledge extraction phase failed: %v\n", err)
	}
	result.KnowledgeExtracted = extracted

	// Phase 8: Review today's conversations and create a daily summary
	reviewed, err := s.phaseReview(agentID)
	if err != nil {
		fmt.Printf("[dream] Warning: review phase failed: %v\n", err)
	}
	result.Reviewed = reviewed

	if s.vecIndex != nil {
		s.rebuildVecIndex()
	}

	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM memories WHERE agent_id = ?", agentID).Scan(&count)
	result.TotalMemories = count

	result.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	fmt.Printf("[dream] Dream cycle for %s complete: expired=%d decayed=%d consolidated=%d reembedded=%d researched=%d reflections=%d knowledge=%d total=%d\n",
		agentID, result.Expired, result.Decayed, result.Consolidated, result.Reembedded, result.Researched, result.Reflections, result.KnowledgeExtracted, result.TotalMemories)

	return result, nil
}

// DistinctAgentIDs returns all unique agent IDs that have memories.
func (s *Store) DistinctAgentIDs() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT agent_id FROM memories WHERE agent_id != ''")
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

// --- Phase 1: Expire ---

func (s *Store) phaseExpire(agentID string) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		"DELETE FROM memories WHERE agent_id = ? AND expires_at != '' AND expires_at <= ?", agentID, now,
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

func (s *Store) phaseDecay(agentID string) (int, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -decayAgeDays).Format(time.RFC3339)

	_, err := s.db.Exec(`
		UPDATE memories SET
			importance = importance * (1.0 - ?),
			modified_at = ?
		WHERE agent_id = ?
		  AND (last_accessed_at < ? OR last_accessed_at = '')
		  AND modified_at < ?
		  AND importance > ?`,
		decayRate,
		time.Now().UTC().Format(time.RFC3339),
		agentID,
		cutoff, cutoff,
		pruneThreshold,
	)
	if err != nil {
		return 0, err
	}

	res, err := s.db.Exec(
		"DELETE FROM memories WHERE agent_id = ? AND importance <= ? AND importance > 0", agentID, pruneThreshold,
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

func (s *Store) phaseConsolidate(agentID string) (int, error) {
	rows, err := s.db.Query(`
		SELECT namespace, COUNT(*) as cnt
		FROM memories
		WHERE agent_id = ?
		GROUP BY namespace
		HAVING cnt > ?`, agentID, consolidateThreshold,
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
		merged, err := s.consolidateNamespace(agentID, ns)
		if err != nil {
			fmt.Printf("[dream] Warning: consolidation of %s failed: %v\n", ns, err)
			continue
		}
		total += merged
	}
	return total, nil
}

func (s *Store) consolidateNamespace(agentID, namespace string) (int, error) {
	memories, err := s.ListMemories(MemoryListOpts{
		AgentID:   agentID,
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
			AgentID:   agentID,
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

func (s *Store) phaseResearch(agentID string) (int, error) {
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		WHERE agent_id = ? AND namespace != '_system' AND namespace != 'inferred'
		ORDER BY importance DESC
		LIMIT 200`, agentID,
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
			AgentID:    agentID,
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

func (s *Store) phaseReembed(agentID string) (int, error) {
	rows, err := s.db.Query(
		"SELECT id, key, content FROM memories WHERE agent_id = ? AND (embedding = '' OR embedding IS NULL)", agentID,
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

func (s *Store) phaseReflect(agentID string) (int, error) {
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		WHERE agent_id = ? AND namespace != '_system'
		ORDER BY namespace, importance DESC
		LIMIT 200`, agentID,
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

	// Step 1: Infer new factual knowledge from existing memories
	inferPrompt := fmt.Sprintf(`You are analyzing an AI agent's memory store. Your job is to find implicit FACTUAL knowledge that can be inferred from what's already stored.

Focus on inferring:
- Domain and industry context (e.g. if user asked about "restaurant payments" and "ReadyToPay", infer they work in restaurant tech)
- Relationships between entities mentioned across different memories
- The user's role and expertise level (e.g. technical vocabulary suggests engineering background)
- Project context and goals that connect separate conversations
- Communication style and preferences of the user (e.g. "uses casual tone", "prefers concise answers")

These are facts ABOUT the user — store them as observations, not as instructions for the agent to copy.

Current memories:
%s

For each inference, output a JSON line with: "namespace", "key", "content", "confidence" (0.0-1.0).
Only include factual inferences you're reasonably confident about (>0.5).
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
					AgentID:    agentID,
					Namespace:  ns,
					Key:        inf.Key,
					Content:    inf.Content + fmt.Sprintf(" [inferred, confidence: %.1f]", inf.Confidence),
					Importance: float64(inf.Confidence) * 0.6,
				})
				count++
			}
		}
		if count > 0 {
			fmt.Printf("[dream] Inferred %d new memories from existing data\n", count)
		}
	}

	// Step 2: Generate behavioral guidance that develops the agent's OWN personality
	// Load previous reflection so the LLM can refine rather than start from scratch
	var prevReflection string
	s.db.QueryRow(
		`SELECT content FROM memories WHERE agent_id = ? AND namespace = '_system' AND key = 'dream-reflection'`,
		agentID,
	).Scan(&prevReflection)

	prevSection := ""
	if prevReflection != "" {
		prevSection = fmt.Sprintf(`
Your previous guidance (refine and evolve — keep what still holds, drop what's no longer supported by evidence, add new insights):
%s

`, prevReflection)
	}

	guidePrompt := fmt.Sprintf(`You are helping an AI agent develop its own authentic personality and interaction style. Review its memory store and generate guidance.

CRITICAL DISTINCTION: Knowing about the user is NOT the same as copying the user.
- "User uses casual tone with emojis" is a useful fact — but does NOT mean the agent should use emojis
- "User prefers concise answers" means keep responses tight — that IS actionable
- The agent should develop its OWN authentic voice, not mirror the user's personality
- Never suggest asking the user questions or probing for information

Generate guidance in two categories:

UNDERSTANDING THE USER (adapt to their needs, not their style):
- How to match the user's expertise level (e.g. "User has deep technical knowledge — skip basic explanations")
- What the user cares about (e.g. "User is focused on scalability — prioritize that in recommendations")
- How the user prefers to receive information (e.g. "User values brevity — lead with the answer")

AGENT PERSONALITY (develop your own character):
- The agent should have its own distinct voice and opinions
- Suggest traits that complement the user — if the user is casual, the agent might be warm but thorough; if the user moves fast, the agent might be the one who catches edge cases
- The agent should feel like a knowledgeable colleague, not a mirror
%s
Current memories:
%s

Output 3-5 guidance statements as a plain text list, one per line starting with "- ".
Be specific and actionable.`, prevSection, dump.String())

	guideResp, err := s.llm.Generate(
		"You are helping an AI develop its own personality. The agent should NOT mirror the user. Output guidance as a plain text list.",
		guidePrompt, 0.3, 1024,
	)
	if err != nil {
		fmt.Printf("[dream] Warning: guidance generation failed: %v\n", err)
	} else {
		s.CreateMemory(&Memory{
			AgentID:    agentID,
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

// --- Phase 7b: Extract Knowledge ---
// Scan recent memories for factual triples and store them in the knowledge graph.

func (s *Store) phaseExtractKnowledge(agentID string) (int, error) {
	if s.facts == nil {
		return 0, nil // knowledge store not wired up — skip silently
	}

	// Use memories created or modified in the last 24 hours as the source material,
	// excluding system/meta namespaces that are dream-generated.
	rows, err := s.db.Query(`
		SELECT namespace, key, content
		FROM memories
		WHERE agent_id = ?
		  AND namespace NOT IN ('_system', 'inferred', 'researched', 'daily_summaries')
		  AND modified_at >= ?
		ORDER BY importance DESC
		LIMIT 100`,
		agentID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339),
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

	prompt := fmt.Sprintf(`Extract structured knowledge facts from the following agent memories.
Each fact should be a (subject, predicate, object) triple representing a concrete, verifiable relationship.

Focus on:
- People and their roles (e.g. "Alice", "is", "backend engineer")
- Technologies and their usage (e.g. "project-x", "uses", "PostgreSQL")
- Ownership and responsibility (e.g. "Bob", "owns", "payment-service")
- Preferences and patterns (e.g. "user", "prefers", "concise answers")
- Project relationships (e.g. "auth-service", "depends-on", "Redis")

Rules:
- Only extract facts clearly supported by the memories — do not speculate
- Skip vague or subjective statements that can't be expressed as clean triples
- Use lowercase, short identifiers for subjects and objects
- Use verb phrases for predicates (e.g. "works-on", "is", "prefers", "uses", "depends-on")
- If a fact was already likely extracted before, skip it — focus on NEW information

Memories:
%s

Output one JSON object per line: {"subject": "...", "predicate": "...", "object": "...", "confidence": 0.0-1.0}
Output ONLY JSON lines, nothing else.`, dump.String())

	resp, err := s.llm.Generate(
		"You are a knowledge extraction system. Output only JSON lines with subject/predicate/object/confidence fields.",
		prompt, 0.1, 4096,
	)
	if err != nil {
		return 0, fmt.Errorf("knowledge extraction LLM call failed: %w", err)
	}

	type triple struct {
		Subject    string  `json:"subject"`
		Predicate  string  `json:"predicate"`
		Object     string  `json:"object"`
		Confidence float64 `json:"confidence"`
	}

	count := 0
	for _, line := range strings.Split(resp.Response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var t triple
		if err := json.Unmarshal([]byte(line), &t); err != nil {
			continue
		}
		if t.Subject == "" || t.Predicate == "" || t.Object == "" || t.Confidence < 0.5 {
			continue
		}
		if err := s.facts.CreateFactRaw(agentID, t.Subject, t.Predicate, t.Object, t.Confidence); err != nil {
			fmt.Printf("[dream] Warning: failed to store fact (%s %s %s): %v\n", t.Subject, t.Predicate, t.Object, err)
			continue
		}
		count++
	}

	if count > 0 {
		fmt.Printf("[dream] Extracted %d knowledge facts from recent memories\n", count)
	}
	return count, nil
}

// --- Phase 8: Review ---
// Summarize today's conversations and store as a daily summary memory.
// Deletes raw chat logs after summarization.

func (s *Store) phaseReview(agentID string) (int, error) {
	// Load chat logs from the last 24 hours
	since := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	logs, err := s.ListChatLogs(agentID, since, 500)
	if err != nil {
		return 0, fmt.Errorf("listing chat logs: %w", err)
	}
	if len(logs) == 0 {
		return 0, nil
	}

	// Group by chat_id and build a conversation dump
	chatGroups := make(map[string][]ChatLog)
	for _, cl := range logs {
		chatGroups[cl.ChatID] = append(chatGroups[cl.ChatID], cl)
	}

	var dump strings.Builder
	for chatID, msgs := range chatGroups {
		dump.WriteString(fmt.Sprintf("\n## Conversation: %s\n", chatID))
		for _, m := range msgs {
			dump.WriteString(fmt.Sprintf("[%s] %s\n", m.Role, m.Content))
		}
	}

	if s.llm == nil {
		// Without LLM, just store the raw dump as the summary
		s.CreateMemory(&Memory{
			AgentID:    agentID,
			Namespace:  "daily_summaries",
			Key:        time.Now().UTC().Format("2006-01-02"),
			Content:    dump.String(),
			Importance: 0.7,
		})
		s.DeleteChatLogsBefore(agentID, time.Now().UTC().Format(time.RFC3339))
		return len(chatGroups), nil
	}

	prompt := fmt.Sprintf(`Summarize today's conversations for an AI agent's daily review.

For each conversation, note:
- What the user asked for
- Key decisions made
- Unresolved questions or follow-ups needed
- Any new information learned about the user

Be concise but thorough. This summary will be stored as a memory for future reference.

Today's conversations (%d conversations, %d messages):
%s`, len(chatGroups), len(logs), dump.String())

	resp, err := s.llm.Generate(
		"You are summarizing an AI agent's daily conversations. Be concise and factual.",
		prompt, 0.2, 2048,
	)
	if err != nil {
		return 0, fmt.Errorf("LLM review: %w", err)
	}

	summary := strings.TrimSpace(resp.Response)
	if summary == "" {
		summary = dump.String() // Fallback to raw dump
	}

	s.CreateMemory(&Memory{
		AgentID:    agentID,
		Namespace:  "daily_summaries",
		Key:        time.Now().UTC().Format("2006-01-02"),
		Content:    summary,
		Importance: 0.7,
	})

	// Clean up processed chat logs
	deleted, _ := s.DeleteChatLogsBefore(agentID, time.Now().UTC().Format(time.RFC3339))
	fmt.Printf("[dream] Reviewed %d conversations (%d messages), deleted %d log entries\n",
		len(chatGroups), len(logs), deleted)

	return len(chatGroups), nil
}
