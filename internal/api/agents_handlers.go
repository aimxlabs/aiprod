package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/agents"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterAgentsRoutes(r chi.Router, store *agents.Store) {
	r.Route("/agent-messages", func(r chi.Router) {
		r.Post("/", s.handleAgentMsgSend(store))
		r.Get("/", s.handleAgentMsgList(store))
		r.Get("/inbox", s.handleAgentInbox(store))
		r.Get("/{id}", s.handleAgentMsgGet(store))
		r.Post("/{id}/read", s.handleAgentMsgRead(store))
	})
	r.Route("/channels", func(r chi.Router) {
		r.Post("/", s.handleChannelCreate(store))
		r.Get("/", s.handleChannelList(store))
		r.Get("/{id}", s.handleChannelGet(store))
	})
	r.Route("/protocols", func(r chi.Router) {
		r.Post("/", s.handleProtocolCreate(store))
		r.Get("/", s.handleProtocolList(store))
	})
	r.Route("/profiles", func(r chi.Router) {
		r.Post("/", s.handleProfileCreate(store))
		r.Get("/", s.handleProfileList(store))
		r.Get("/active", s.handleProfileGetActive(store))
		r.Post("/{id}/activate", s.handleProfileActivate(store))
	})
}

func (s *Server) handleAgentMsgSend(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m agents.Message
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if m.FromAgent == "" { m.FromAgent = GetAgentID(r) }
		if m.ToAgent == "" || m.Body == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "to_agent and body are required")
			return
		}
		result, err := store.SendMessage(&m)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleAgentMsgList(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := agents.MessageListOpts{
			ToAgent:   r.URL.Query().Get("to"),
			FromAgent: r.URL.Query().Get("from"),
			Channel:   r.URL.Query().Get("channel"),
			Status:    r.URL.Query().Get("status"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListMessages(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleAgentInbox(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent_id")
		if agentID == "" { agentID = GetAgentID(r) }
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.Inbox(agentID, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleAgentMsgGet(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, err := store.GetMessage(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if m == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Message not found")
			return
		}
		WriteJSON(w, http.StatusOK, m)
	}
}

func (s *Server) handleAgentMsgRead(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.MarkRead(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"read": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleChannelCreate(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ch agents.Channel
		if err := json.NewDecoder(r.Body).Decode(&ch); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if ch.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.CreateChannel(&ch)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleChannelList(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListChannels()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleChannelGet(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, err := store.GetChannel(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if ch == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Channel not found")
			return
		}
		WriteJSON(w, http.StatusOK, ch)
	}
}

func (s *Server) handleProtocolCreate(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p agents.Protocol
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if p.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		p.Enabled = true
		result, err := store.CreateProtocol(&p)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleProtocolList(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListProtocols()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleProfileCreate(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var bp agents.BehaviorProfile
		if err := json.NewDecoder(r.Body).Decode(&bp); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if bp.AgentID == "" { bp.AgentID = GetAgentID(r) }
		if bp.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.CreateProfile(&bp)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleProfileList(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListProfiles(r.URL.Query().Get("agent_id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleProfileGetActive(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := r.URL.Query().Get("agent_id")
		if agentID == "" { agentID = GetAgentID(r) }
		bp, err := store.GetActiveProfile(agentID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if bp == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "No active profile")
			return
		}
		WriteJSON(w, http.StatusOK, bp)
	}
}

func (s *Server) handleProfileActivate(store *agents.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := GetAgentID(r)
		if err := store.ActivateProfile(agentID, chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"activated": chi.URLParam(r, "id")})
	}
}
