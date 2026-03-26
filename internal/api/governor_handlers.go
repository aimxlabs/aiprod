package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/governor"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterGovernorRoutes(r chi.Router, store *governor.Store) {
	r.Route("/budgets", func(r chi.Router) {
		r.Post("/", s.handleBudgetCreate(store))
		r.Get("/", s.handleBudgetList(store))
		r.Get("/{id}", s.handleBudgetGet(store))
		r.Post("/spend", s.handleBudgetSpend(store))
		r.Get("/{id}/events", s.handleBudgetEvents(store))
	})
	r.Route("/prompts", func(r chi.Router) {
		r.Post("/", s.handlePromptCreate(store))
		r.Get("/", s.handlePromptList(store))
		r.Get("/{name}/active", s.handlePromptGetActive(store))
		r.Get("/{name}/versions/{version}", s.handlePromptGetVersion(store))
		r.Post("/{name}/activate/{version}", s.handlePromptActivate(store))
	})
	r.Route("/strategies", func(r chi.Router) {
		r.Post("/", s.handleStrategyCreate(store))
		r.Get("/", s.handleStrategyList(store))
		r.Get("/{id}", s.handleStrategyGet(store))
		r.Patch("/{id}", s.handleStrategyUpdate(store))
	})
}

func (s *Server) handleBudgetCreate(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var b governor.Budget
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if b.AgentID == "" { b.AgentID = GetAgentID(r) }
		if b.ResourceType == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "resource_type is required")
			return
		}
		result, err := store.CreateBudget(&b)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleBudgetList(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListBudgets(r.URL.Query().Get("agent_id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleBudgetGet(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := store.GetBudget(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if b == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Budget not found")
			return
		}
		WriteJSON(w, http.StatusOK, b)
	}
}

func (s *Server) handleBudgetSpend(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AgentID      string  `json:"agent_id"`
			ResourceType string  `json:"resource_type"`
			Amount       float64 `json:"amount"`
			Description  string  `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.AgentID == "" { req.AgentID = GetAgentID(r) }
		remaining, alert, err := store.Spend(req.AgentID, req.ResourceType, req.Amount, req.Description)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BUDGET_ERROR", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"remaining": remaining,
			"alert":     alert,
		})
	}
}

func (s *Server) handleBudgetEvents(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.GetBudgetEvents(chi.URLParam(r, "id"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handlePromptCreate(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p governor.PromptVersion
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if p.Name == "" || p.Content == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name and content are required")
			return
		}
		if p.CreatedBy == "" { p.CreatedBy = GetAgentID(r) }
		result, err := store.CreatePrompt(&p)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handlePromptList(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListPrompts(r.URL.Query().Get("name"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handlePromptGetActive(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := store.GetActivePrompt(chi.URLParam(r, "name"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if p == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "No active prompt found")
			return
		}
		WriteJSON(w, http.StatusOK, p)
	}
}

func (s *Server) handlePromptGetVersion(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ver, _ := strconv.Atoi(chi.URLParam(r, "version"))
		p, err := store.GetPromptVersion(chi.URLParam(r, "name"), ver)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if p == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Prompt version not found")
			return
		}
		WriteJSON(w, http.StatusOK, p)
	}
}

func (s *Server) handlePromptActivate(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ver, _ := strconv.Atoi(chi.URLParam(r, "version"))
		if err := store.ActivatePrompt(chi.URLParam(r, "name"), ver); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"activated": chi.URLParam(r, "name"), "version": ver})
	}
}

func (s *Server) handleStrategyCreate(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var st governor.Strategy
		if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if st.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.CreateStrategy(&st)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleStrategyList(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListStrategies(r.URL.Query().Get("type"), r.URL.Query().Get("agent_id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleStrategyGet(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st, err := store.GetStrategy(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if st == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Strategy not found")
			return
		}
		WriteJSON(w, http.StatusOK, st)
	}
}

func (s *Server) handleStrategyUpdate(store *governor.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdateStrategy(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		st, _ := store.GetStrategy(id)
		WriteJSON(w, http.StatusOK, st)
	}
}
