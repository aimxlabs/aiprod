package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/observe"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterObserveRoutes(r chi.Router, store *observe.Store) {
	r.Route("/traces", func(r chi.Router) {
		r.Post("/", s.handleTraceStart(store))
		r.Get("/", s.handleTraceList(store))
		r.Get("/{id}", s.handleTraceGet(store))
		r.Post("/{id}/end", s.handleTraceEnd(store))
		r.Post("/{id}/steps", s.handleStepAdd(store))
		r.Get("/{id}/steps", s.handleStepList(store))
		r.Post("/{id}/snapshots", s.handleSnapshotSave(store))
		r.Get("/{id}/snapshots", s.handleSnapshotList(store))
		r.Get("/{id}/stats", s.handleTraceAgentStats(store))
	})
	r.Route("/failures", func(r chi.Router) {
		r.Post("/", s.handleFailureRecord(store))
		r.Get("/", s.handleFailureList(store))
	})
}

func (s *Server) handleTraceStart(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var t observe.Trace
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if t.AgentID == "" { t.AgentID = GetAgentID(r) }
		if t.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.StartTrace(&t)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleTraceEnd(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Status     string  `json:"status"`
			Output     string  `json:"output"`
			Error      string  `json:"error"`
			TokenCount int     `json:"token_count"`
			Cost       float64 `json:"cost"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Status == "" { req.Status = "completed" }
		id := chi.URLParam(r, "id")
		if err := store.EndTrace(id, req.Status, req.Output, req.Error, req.TokenCount, req.Cost); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		t, _ := store.GetTrace(id)
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleTraceGet(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := store.GetTrace(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Trace not found")
			return
		}
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleTraceList(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := observe.TraceListOpts{
			AgentID:   r.URL.Query().Get("agent_id"),
			SessionID: r.URL.Query().Get("session_id"),
			Status:    r.URL.Query().Get("status"),
			TraceType: r.URL.Query().Get("trace_type"),
			ParentID:  r.URL.Query().Get("parent_id"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListTraces(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleStepAdd(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var step observe.TraceStep
		if err := json.NewDecoder(r.Body).Decode(&step); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		step.TraceID = chi.URLParam(r, "id")
		result, err := store.AddStep(&step)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleStepList(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.GetSteps(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleSnapshotSave(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			StepSeq int    `json:"step_seq"`
			State   string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		result, err := store.SaveSnapshot(chi.URLParam(r, "id"), req.StepSeq, req.State)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleSnapshotList(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.GetSnapshots(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleTraceAgentStats(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.AgentStats(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleFailureRecord(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var fp observe.FailurePattern
		if err := json.NewDecoder(r.Body).Decode(&fp); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if fp.PatternName == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "pattern_name is required")
			return
		}
		result, err := store.RecordFailure(&fp)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleFailureList(store *observe.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListFailurePatterns(limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
