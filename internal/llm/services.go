package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResolveModel picks the model to use: request override > per-feature config > client default.
func ResolveModel(requestModel string, configStore *ConfigStore, feature string, clientDefault string) string {
	if requestModel != "" {
		return requestModel
	}
	if configStore != nil {
		if fc, err := configStore.GetFeatureConfig(feature); err == nil && fc != nil {
			return fc.Model
		}
	}
	return clientDefault
}

// ResolveParams picks model, temperature, and max_tokens from the resolution chain.
func ResolveParams(requestModel string, configStore *ConfigStore, feature string, client *Client, defaultTemp float64, defaultMaxTokens int) (model string, temp float64, maxTokens int) {
	model = client.Model
	temp = defaultTemp
	maxTokens = defaultMaxTokens

	// Layer 2: per-feature config from DB
	if configStore != nil {
		if fc, err := configStore.GetFeatureConfig(feature); err == nil && fc != nil {
			model = fc.Model
			if fc.Temperature >= 0 { temp = fc.Temperature }
			if fc.MaxTokens > 0 { maxTokens = fc.MaxTokens }
		}
	}

	// Layer 1: per-request override (highest priority)
	if requestModel != "" {
		model = requestModel
	}

	return
}

// CompressText summarizes text, returning a compressed version.
func (c *Client) CompressText(text string, maxLen int, model string, configStore *ConfigStore) (summary string, err error) {
	if maxLen <= 0 {
		maxLen = 200
	}
	m, temp, maxTok := ResolveParams(model, configStore, FeatureCompress, c, 0.1, maxLen*2)

	system := "You are a precise text compressor. Preserve all key facts, decisions, and action items. Output ONLY the compressed text, nothing else."
	prompt := fmt.Sprintf("Compress the following text to at most %d words while preserving all important information:\n\n%s", maxLen, text)

	resp, err := c.GenerateWith(m, system, prompt, temp, maxTok)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Response), nil
}

// ExtractFacts extracts subject-predicate-object triples from text.
func (c *Client) ExtractFacts(text string, model string, configStore *ConfigStore) ([]FactTriple, error) {
	m, temp, maxTok := ResolveParams(model, configStore, FeatureExtractFacts, c, 0.1, 2000)

	system := `You extract structured facts from text. Output a JSON array of objects with fields: "subject", "predicate", "object", "confidence" (0-1). Only output the JSON array, nothing else.`
	prompt := fmt.Sprintf("Extract all factual claims as subject-predicate-object triples from this text:\n\n%s", text)

	resp, err := c.GenerateWith(m, system, prompt, temp, maxTok)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(resp.Response)
	raw = stripCodeFences(raw)

	var facts []FactTriple
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return nil, fmt.Errorf("parsing facts JSON: %w (raw: %s)", err, truncate(raw, 200))
	}
	return facts, nil
}

type FactTriple struct {
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
}

// InferSchema analyzes sample data and returns a JSON schema description.
func (c *Client) InferSchema(sampleData string, sourceType string, model string, configStore *ConfigStore) (schema string, err error) {
	m, temp, maxTok := ResolveParams(model, configStore, FeatureInferSchema, c, 0.1, 2000)

	system := `You are a schema inference engine. Analyze the provided data samples and output a JSON schema describing the structure. Include field names, types, whether they're required, and any patterns you observe. Output ONLY valid JSON.`
	prompt := fmt.Sprintf("Infer the schema for this %s data:\n\n%s", sourceType, sampleData)

	resp, err := c.GenerateWith(m, system, prompt, temp, maxTok)
	if err != nil {
		return "", err
	}
	raw := strings.TrimSpace(resp.Response)
	raw = stripCodeFences(raw)
	return raw, nil
}

// GenerateReflection produces a post-mortem reflection on a completed trace.
func (c *Client) GenerateReflection(traceSummary string, model string, configStore *ConfigStore) (content string, lessons []string, score float64, err error) {
	m, temp, maxTok := ResolveParams(model, configStore, FeatureReflect, c, 0.3, 1000)

	system := `You analyze completed task traces and produce reflections. Output JSON with fields:
- "content": a paragraph reflecting on what happened, what went well, and what could improve
- "lessons": an array of 1-5 short lesson strings
- "score": a 0-1 quality score for how well the task was executed
Output ONLY valid JSON.`
	prompt := fmt.Sprintf("Reflect on this completed task trace:\n\n%s", traceSummary)

	resp, err := c.GenerateWith(m, system, prompt, temp, maxTok)
	if err != nil {
		return "", nil, 0, err
	}

	raw := strings.TrimSpace(resp.Response)
	raw = stripCodeFences(raw)

	var result struct {
		Content string   `json:"content"`
		Lessons []string `json:"lessons"`
		Score   float64  `json:"score"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return raw, nil, 0.5, nil
	}
	return result.Content, result.Lessons, result.Score, nil
}

// AnalyzeFailure analyzes an error message and suggests a fix.
func (c *Client) AnalyzeFailure(errorMsg, context string, model string, configStore *ConfigStore) (description, suggestedFix string, err error) {
	m, temp, maxTok := ResolveParams(model, configStore, FeatureAnalyzeFailure, c, 0.2, 500)

	system := `You are a failure analysis engine. Given an error message and context, output JSON with:
- "description": what likely went wrong (1-2 sentences)
- "suggested_fix": a concrete suggestion to prevent this failure (1-2 sentences)
Output ONLY valid JSON.`
	prompt := fmt.Sprintf("Error: %s\n\nContext: %s", errorMsg, context)

	resp, err := c.GenerateWith(m, system, prompt, temp, maxTok)
	if err != nil {
		return "", "", err
	}

	raw := strings.TrimSpace(resp.Response)
	raw = stripCodeFences(raw)

	var result struct {
		Description  string `json:"description"`
		SuggestedFix string `json:"suggested_fix"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return raw, "", nil
	}
	return result.Description, result.SuggestedFix, nil
}

func stripCodeFences(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
