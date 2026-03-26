package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/tools"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterToolsRoutes(r chi.Router, store *tools.Store) {
	r.Route("/tools", func(r chi.Router) {
		r.Post("/", s.handleToolRegister(store))
		r.Get("/", s.handleToolList(store))
		r.Get("/{id}", s.handleToolGet(store))
		r.Patch("/{id}", s.handleToolUpdate(store))
		r.Delete("/{id}", s.handleToolDelete(store))
		r.Post("/{id}/execute", s.handleToolExecute(store))
		r.Get("/{id}/executions", s.handleToolExecList(store))
		r.Post("/{id}/simulate", s.handleToolSimulate(store))
		r.Get("/{id}/simulations", s.handleToolSimList(store))
		r.Post("/simulations/{simId}/approve", s.handleToolSimApprove(store))
	})
}

func (s *Server) handleToolRegister(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var t tools.Tool
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if t.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		t.Enabled = true
		result, err := store.RegisterTool(&t)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleToolList(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := tools.ToolListOpts{
			Category: r.URL.Query().Get("category"),
			Query:    r.URL.Query().Get("q"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		if v := r.URL.Query().Get("enabled"); v != "" {
			b := v == "true" || v == "1"
			opts.Enabled = &b
		}
		result, err := store.ListTools(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleToolGet(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := store.GetTool(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Tool not found")
			return
		}
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleToolUpdate(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdateTool(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		t, _ := store.GetTool(id)
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleToolDelete(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteTool(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleToolExecute(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ex tools.ToolExecution
		if err := json.NewDecoder(r.Body).Decode(&ex); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		ex.ToolID = chi.URLParam(r, "id")
		if ex.AgentID == "" { ex.AgentID = GetAgentID(r) }
		result, err := store.RecordExecution(&ex)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleToolExecList(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListExecutions(chi.URLParam(r, "id"), r.URL.Query().Get("agent_id"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleToolSimulate(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sim tools.Simulation
		if err := json.NewDecoder(r.Body).Decode(&sim); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		sim.ToolID = chi.URLParam(r, "id")
		if sim.AgentID == "" { sim.AgentID = GetAgentID(r) }
		result, err := store.CreateSimulation(&sim)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleToolSimList(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListSimulations(chi.URLParam(r, "id"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleToolSimApprove(store *tools.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.ApproveSimulation(chi.URLParam(r, "simId")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"approved": chi.URLParam(r, "simId")})
	}
}
