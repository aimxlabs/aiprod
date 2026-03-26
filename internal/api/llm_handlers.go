package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/garett/aiprod/internal/knowledge"
	"github.com/garett/aiprod/internal/llm"
	"github.com/garett/aiprod/internal/memory"
	"github.com/garett/aiprod/internal/observe"
	"github.com/garett/aiprod/internal/planner"
	"github.com/go-chi/chi/v5"
)

type LLMStores struct {
	LLM       *llm.Client
	Config    *llm.ConfigStore
	Memory    *memory.Store
	Observe   *observe.Store
	Knowledge *knowledge.Store
	Planner   *planner.Store
}

func (s *Server) RegisterLLMRoutes(r chi.Router, stores *LLMStores) {
	r.Route("/llm", func(r chi.Router) {
		r.Get("/status", s.handleLLMStatus(stores))
		r.Get("/config", s.handleLLMConfigList(stores))
		r.Put("/config/{feature}", s.handleLLMConfigSet(stores))
		r.Delete("/config/{feature}", s.handleLLMConfigDelete(stores))
		r.Post("/compress", s.handleLLMCompress(stores))
		r.Post("/extract-facts", s.handleLLMExtractFacts(stores))
		r.Post("/infer-schema", s.handleLLMInferSchema(stores))
		r.Post("/reflect/{traceId}", s.handleLLMReflect(stores))
		r.Post("/analyze-failure", s.handleLLMAnalyzeFailure(stores))
	})
}

func (s *Server) handleLLMStatus(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := map[string]interface{}{
			"default_model": stores.LLM.Model,
			"url":           stores.LLM.BaseURL,
		}

		if err := stores.LLM.Ping(); err != nil {
			result["available"] = false
			result["error"] = err.Error()
		} else {
			result["available"] = true
		}

		// Include effective config for all features
		if stores.Config != nil {
			if cfg, err := stores.Config.FullConfig(stores.LLM); err == nil {
				result["features"] = cfg
			}
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

// GET /llm/config — list all per-feature model overrides and effective config
func (s *Server) handleLLMConfigList(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		effective, err := stores.Config.FullConfig(stores.LLM)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}

		overrides, _ := stores.Config.ListConfig()
		if overrides == nil { overrides = []llm.FeatureConfig{} }

		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"default_model": stores.LLM.Model,
			"features":      llm.AllFeatures,
			"effective":     effective,
			"overrides":     overrides,
		})
	}
}

// PUT /llm/config/{feature} — set model override for a feature
func (s *Server) handleLLMConfigSet(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feature := chi.URLParam(r, "feature")
		if !validFeature(feature) {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST",
				fmt.Sprintf("invalid feature %q, must be one of: %s", feature, strings.Join(llm.AllFeatures, ", ")))
			return
		}

		var req struct {
			Model       string  `json:"model"`
			Temperature float64 `json:"temperature"`
			MaxTokens   int     `json:"max_tokens"`
		}
		req.Temperature = -1 // sentinel: not set
		req.MaxTokens = -1
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Model == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "model is required")
			return
		}

		if err := stores.Config.SetFeatureConfig(feature, req.Model, req.Temperature, req.MaxTokens); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}

		fc, _ := stores.Config.GetFeatureConfig(feature)
		WriteJSON(w, http.StatusOK, fc)
	}
}

// DELETE /llm/config/{feature} — remove override, revert to default
func (s *Server) handleLLMConfigDelete(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feature := chi.URLParam(r, "feature")
		if err := stores.Config.DeleteFeatureConfig(feature); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"deleted": feature,
			"model":   stores.LLM.Model,
			"source":  "default",
		})
	}
}

// POST /llm/compress — compress text, with optional per-request model override
func (s *Server) handleLLMCompress(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text       string `json:"text"`
			MaxWords   int    `json:"max_words"`
			Model      string `json:"model"`
			AgentID    string `json:"agent_id"`
			SourceType string `json:"source_type"`
			SourceID   string `json:"source_id"`
			Store      bool   `json:"store"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Text == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "text is required")
			return
		}
		if req.AgentID == "" { req.AgentID = GetAgentID(r) }

		summary, err := stores.LLM.CompressText(req.Text, req.MaxWords, req.Model, stores.Config)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "LLM_ERROR", err.Error())
			return
		}

		usedModel := llm.ResolveModel(req.Model, stores.Config, llm.FeatureCompress, stores.LLM.Model)
		result := map[string]interface{}{
			"summary":        summary,
			"original_words": len(strings.Fields(req.Text)),
			"summary_words":  len(strings.Fields(summary)),
			"model_used":     usedModel,
		}

		if req.Store && stores.Memory != nil {
			origWords := len(strings.Fields(req.Text))
			compWords := len(strings.Fields(summary))
			ratio := 0.0
			if origWords > 0 {
				ratio = float64(compWords) / float64(origWords)
			}
			comp := &memory.Compression{
				AgentID:          req.AgentID,
				SourceType:       req.SourceType,
				SourceID:         req.SourceID,
				OriginalTokens:   origWords,
				CompressedTokens: compWords,
				OriginalHash:     fmt.Sprintf("%x", len(req.Text)),
				Summary:          summary,
				CompressionRatio: ratio,
				Method:           "llm_" + usedModel,
			}
			stored, err := stores.Memory.CreateCompression(comp)
			if err == nil {
				result["compression_id"] = stored.ID
			}
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

// POST /llm/extract-facts — extract facts, with optional per-request model override
func (s *Server) handleLLMExtractFacts(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Text    string `json:"text"`
			Model   string `json:"model"`
			AgentID string `json:"agent_id"`
			Store   bool   `json:"store"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Text == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "text is required")
			return
		}
		if req.AgentID == "" { req.AgentID = GetAgentID(r) }

		facts, err := stores.LLM.ExtractFacts(req.Text, req.Model, stores.Config)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "LLM_ERROR", err.Error())
			return
		}

		usedModel := llm.ResolveModel(req.Model, stores.Config, llm.FeatureExtractFacts, stores.LLM.Model)
		result := map[string]interface{}{
			"facts":      facts,
			"count":      len(facts),
			"model_used": usedModel,
		}

		if req.Store && stores.Knowledge != nil {
			var storedIDs []string
			for _, f := range facts {
				fact := &knowledge.Fact{
					AgentID:    req.AgentID,
					Subject:    f.Subject,
					Predicate:  f.Predicate,
					Object:     f.Object,
					Confidence: f.Confidence,
					SourceType: "llm_extraction",
				}
				stored, err := stores.Knowledge.CreateFact(fact)
				if err == nil {
					storedIDs = append(storedIDs, stored.ID)
				}
			}
			result["stored_ids"] = storedIDs
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

// POST /llm/infer-schema — infer schema, with optional per-request model override
func (s *Server) handleLLMInferSchema(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Data       string `json:"data"`
			SourceType string `json:"source_type"`
			SourceID   string `json:"source_id"`
			Model      string `json:"model"`
			Store      bool   `json:"store"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Data == "" || req.SourceType == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "data and source_type are required")
			return
		}

		schema, err := stores.LLM.InferSchema(req.Data, req.SourceType, req.Model, stores.Config)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "LLM_ERROR", err.Error())
			return
		}

		usedModel := llm.ResolveModel(req.Model, stores.Config, llm.FeatureInferSchema, stores.LLM.Model)
		result := map[string]interface{}{
			"inferred_schema": json.RawMessage(schema),
			"source_type":     req.SourceType,
			"model_used":      usedModel,
		}

		if req.Store && stores.Knowledge != nil {
			si := &knowledge.SchemaInference{
				SourceType:     req.SourceType,
				SourceID:       req.SourceID,
				InferredSchema: schema,
				SampleCount:    1,
			}
			stored, err := stores.Knowledge.SaveInference(si)
			if err == nil {
				result["inference_id"] = stored.ID
			}
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

// POST /llm/reflect/{traceId} — reflect on a trace, with optional per-request model override
func (s *Server) handleLLMReflect(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		traceID := chi.URLParam(r, "traceId")

		var req struct {
			Model string `json:"model"`
		}
		// Body is optional for this endpoint
		json.NewDecoder(r.Body).Decode(&req)

		trace, err := stores.Observe.GetTrace(traceID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if trace == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Trace not found")
			return
		}

		steps, _ := stores.Observe.GetSteps(traceID)

		summary := fmt.Sprintf("Task: %s\nType: %s\nStatus: %s\nDuration: %dms\nTokens: %d\n",
			trace.Name, trace.TraceType, trace.Status, trace.DurationMs, trace.TokenCount)
		if trace.Error != "" {
			summary += fmt.Sprintf("Error: %s\n", trace.Error)
		}
		for _, step := range steps {
			summary += fmt.Sprintf("\nStep %d [%s]: %s — %s", step.Seq, step.Status, step.Name, step.StepType)
			if step.Error != "" {
				summary += fmt.Sprintf(" (error: %s)", step.Error)
			}
		}

		content, lessons, score, err := stores.LLM.GenerateReflection(summary, req.Model, stores.Config)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "LLM_ERROR", err.Error())
			return
		}

		ref := &planner.Reflection{
			AgentID:        trace.AgentID,
			SourceType:     "trace",
			SourceID:       traceID,
			ReflectionType: "llm_post_mortem",
			Content:        content,
			Lessons:        lessons,
			Score:          score,
		}
		stored, storeErr := stores.Planner.CreateReflection(ref)

		usedModel := llm.ResolveModel(req.Model, stores.Config, llm.FeatureReflect, stores.LLM.Model)
		result := map[string]interface{}{
			"content":    content,
			"lessons":    lessons,
			"score":      score,
			"model_used": usedModel,
		}
		if storeErr == nil {
			result["reflection_id"] = stored.ID
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

// POST /llm/analyze-failure — analyze error, with optional per-request model override
func (s *Server) handleLLMAnalyzeFailure(stores *LLMStores) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Error       string `json:"error"`
			Context     string `json:"context"`
			Model       string `json:"model"`
			PatternName string `json:"pattern_name"`
			TraceID     string `json:"trace_id"`
			Store       bool   `json:"store"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Error == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "error is required")
			return
		}

		description, suggestedFix, err := stores.LLM.AnalyzeFailure(req.Error, req.Context, req.Model, stores.Config)
		if err != nil {
			WriteError(w, http.StatusBadGateway, "LLM_ERROR", err.Error())
			return
		}

		usedModel := llm.ResolveModel(req.Model, stores.Config, llm.FeatureAnalyzeFailure, stores.LLM.Model)
		result := map[string]interface{}{
			"description":   description,
			"suggested_fix": suggestedFix,
			"model_used":    usedModel,
		}

		if req.Store && stores.Observe != nil && req.PatternName != "" {
			fp := &observe.FailurePattern{
				PatternName:  req.PatternName,
				Description:  description,
				ErrorRegex:   req.Error,
				LastTraceID:  req.TraceID,
				SuggestedFix: suggestedFix,
				Occurrences:  1,
			}
			stored, err := stores.Observe.RecordFailure(fp)
			if err == nil {
				result["failure_pattern_id"] = stored.ID
			}
		}

		WriteJSON(w, http.StatusOK, result)
	}
}

func validFeature(f string) bool {
	for _, v := range llm.AllFeatures {
		if v == f { return true }
	}
	return false
}
