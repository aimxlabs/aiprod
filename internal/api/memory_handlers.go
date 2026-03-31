package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/memory"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterMemoryRoutes(r chi.Router, store *memory.Store) {
	r.Route("/memory", func(r chi.Router) {
		r.Post("/", s.handleMemoryCreate(store))
		r.Get("/", s.handleMemoryList(store))
		r.Get("/{id}", s.handleMemoryGet(store))
		r.Patch("/{id}", s.handleMemoryUpdate(store))
		r.Delete("/{id}", s.handleMemoryDelete(store))
		r.Post("/dream", s.handleDream(store))
		r.Post("/chat-log", s.handleChatLogCreate(store))
		r.Get("/chat-log", s.handleChatLogList(store))
	})
	r.Route("/scratchpad", func(r chi.Router) {
		r.Post("/", s.handleScratchSet(store))
		r.Get("/", s.handleScratchList(store))
		r.Get("/{id}", s.handleScratchGet(store))
		r.Delete("/{id}", s.handleScratchDelete(store))
		r.Post("/cleanup", s.handleScratchCleanup(store))
	})
	r.Route("/compressions", func(r chi.Router) {
		r.Post("/", s.handleCompressionCreate(store))
		r.Get("/", s.handleCompressionList(store))
	})
}

func (s *Server) handleMemoryCreate(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m memory.Memory
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		m.AgentID = GetAgentID(r) // Always scope to authenticated agent
		if m.Key == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "key is required")
			return
		}
		result, err := store.CreateMemory(&m)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleMemoryList(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := memory.MemoryListOpts{
			AgentID:   GetAgentID(r), // Always scope to authenticated agent
			Namespace: r.URL.Query().Get("namespace"),
			SourceType: r.URL.Query().Get("source_type"),
			Query:     r.URL.Query().Get("q"),
		}
		if v := r.URL.Query().Get("importance_min"); v != "" {
			opts.ImportanceMin, _ = strconv.ParseFloat(v, 64)
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			opts.Limit, _ = strconv.Atoi(v)
		}
		result, err := store.ListMemories(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleMemoryGet(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := store.GetMemory(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if m == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Memory not found")
			return
		}
		WriteJSON(w, http.StatusOK, m)
	}
}

func (s *Server) handleMemoryUpdate(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.UpdateMemory(chi.URLParam(r, "id"), updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		m, _ := store.GetMemory(chi.URLParam(r, "id"))
		WriteJSON(w, http.StatusOK, m)
	}
}

func (s *Server) handleMemoryDelete(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteMemory(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleScratchSet(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AgentID   string `json:"agent_id"`
			SessionID string `json:"session_id"`
			Key       string `json:"key"`
			Value     string `json:"value"`
			TTL       int    `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		req.AgentID = GetAgentID(r) // Always scope to authenticated agent
		if req.Key == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "key is required")
			return
		}
		result, err := store.SetScratch(req.AgentID, req.SessionID, req.Key, req.Value, req.TTL)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleScratchList(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListScratch(GetAgentID(r), r.URL.Query().Get("session_id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleScratchGet(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := store.GetScratch(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if e == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Scratch entry not found or expired")
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}

func (s *Server) handleScratchDelete(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteScratch(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleScratchCleanup(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		count, err := store.CleanupScratch()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int64{"cleaned": count})
	}
}

func (s *Server) handleCompressionCreate(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var c memory.Compression
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		c.AgentID = GetAgentID(r) // Always scope to authenticated agent
		result, err := store.CreateCompression(&c)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleCompressionList(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListCompressions(GetAgentID(r), r.URL.Query().Get("source_type"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleDream(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := GetAgentID(r)
		result, err := store.Dream(agentID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "DREAM_FAILED", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleChatLogCreate(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChatID  string `json:"chat_id"`
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.ChatID == "" || req.Role == "" || req.Content == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "chat_id, role, and content are required")
			return
		}
		cl, err := store.CreateChatLog(GetAgentID(r), req.ChatID, req.Role, req.Content)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, cl)
	}
}

func (s *Server) handleChatLogList(store *memory.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListChatLogs(GetAgentID(r), r.URL.Query().Get("since"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
