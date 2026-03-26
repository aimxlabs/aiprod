package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) registerAuthRoutes(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/agents", s.handleCreateAgent)
		r.Get("/agents", s.handleListAgents)
		r.Post("/keys", s.handleCreateKey)
		r.Get("/keys", s.handleListKeys)
		r.Delete("/keys/{id}", s.handleRevokeKey)
		r.Get("/audit", s.handleQueryAudit)
	})
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON body")
		return
	}
	if req.Name == "" {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}

	agent, err := s.AuthStore.CreateAgent(req.Name, req.Description)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	s.AuthStore.LogAudit(GetAgentID(r), "auth.create_agent", agent.ID, "", r.RemoteAddr)
	WriteJSON(w, http.StatusCreated, agent)
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := s.AuthStore.ListAgents()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, agents)
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AgentID   string   `json:"agent_id"`
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON body")
		return
	}
	if req.AgentID == "" || len(req.Scopes) == 0 {
		WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "agent_id and scopes are required")
		return
	}

	key, rawKey, err := s.AuthStore.CreateAPIKey(req.AgentID, req.Name, req.Scopes, req.ExpiresAt)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}

	s.AuthStore.LogAudit(GetAgentID(r), "auth.create_key", key.ID, "", r.RemoteAddr)
	WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"key":     key,
		"raw_key": rawKey,
	})
}

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	keys, err := s.AuthStore.ListKeys(agentID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, keys)
}

func (s *Server) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.AuthStore.RevokeKey(id); err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	s.AuthStore.LogAudit(GetAgentID(r), "auth.revoke_key", id, "", r.RemoteAddr)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *Server) handleQueryAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, err := s.AuthStore.QueryAudit(q.Get("agent_id"), q.Get("action"), q.Get("since"), 100)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, entries)
}
