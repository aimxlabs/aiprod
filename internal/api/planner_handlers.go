package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/planner"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterPlannerRoutes(r chi.Router, store *planner.Store) {
	r.Route("/plans", func(r chi.Router) {
		r.Post("/", s.handlePlanCreate(store))
		r.Get("/", s.handlePlanList(store))
		r.Get("/{id}", s.handlePlanGet(store))
		r.Patch("/{id}", s.handlePlanUpdate(store))
		r.Delete("/{id}", s.handlePlanDelete(store))
		r.Post("/{id}/steps", s.handlePlanStepAdd(store))
		r.Get("/{id}/steps", s.handlePlanStepList(store))
		r.Patch("/steps/{stepId}", s.handlePlanStepUpdate(store))
	})
	r.Route("/reflections", func(r chi.Router) {
		r.Post("/", s.handleReflectionCreate(store))
		r.Get("/", s.handleReflectionList(store))
		r.Get("/{id}", s.handleReflectionGet(store))
	})
}

func (s *Server) handlePlanCreate(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p planner.Plan
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if p.AgentID == "" { p.AgentID = GetAgentID(r) }
		if p.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.CreatePlan(&p)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handlePlanList(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := planner.PlanListOpts{
			AgentID:  r.URL.Query().Get("agent_id"),
			Status:   r.URL.Query().Get("status"),
			ParentID: r.URL.Query().Get("parent_id"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListPlans(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handlePlanGet(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		includeSteps := r.URL.Query().Get("steps") != "false"
		p, err := store.GetPlan(chi.URLParam(r, "id"), includeSteps)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if p == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Plan not found")
			return
		}
		WriteJSON(w, http.StatusOK, p)
	}
}

func (s *Server) handlePlanUpdate(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdatePlan(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		p, _ := store.GetPlan(id, false)
		WriteJSON(w, http.StatusOK, p)
	}
}

func (s *Server) handlePlanDelete(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeletePlan(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handlePlanStepAdd(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var step planner.PlanStep
		if err := json.NewDecoder(r.Body).Decode(&step); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		step.PlanID = chi.URLParam(r, "id")
		result, err := store.AddStep(&step)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handlePlanStepList(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.GetSteps(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handlePlanStepUpdate(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.UpdateStep(chi.URLParam(r, "stepId"), updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"updated": chi.URLParam(r, "stepId")})
	}
}

func (s *Server) handleReflectionCreate(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var ref planner.Reflection
		if err := json.NewDecoder(r.Body).Decode(&ref); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if ref.AgentID == "" { ref.AgentID = GetAgentID(r) }
		if ref.Content == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "content is required")
			return
		}
		result, err := store.CreateReflection(&ref)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleReflectionList(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := planner.ReflectionListOpts{
			AgentID:        r.URL.Query().Get("agent_id"),
			SourceType:     r.URL.Query().Get("source_type"),
			SourceID:       r.URL.Query().Get("source_id"),
			ReflectionType: r.URL.Query().Get("type"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListReflections(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleReflectionGet(store *planner.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref, err := store.GetReflection(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if ref == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Reflection not found")
			return
		}
		WriteJSON(w, http.StatusOK, ref)
	}
}
