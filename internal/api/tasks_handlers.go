package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/tasks"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterTasksRoutes(r chi.Router, store *tasks.Store) {
	r.Route("/tasks", func(r chi.Router) {
		r.Post("/", s.handleTaskCreate(store))
		r.Get("/", s.handleTaskList(store))
		r.Get("/{id}", s.handleTaskGet(store))
		r.Patch("/{id}", s.handleTaskUpdate(store))
		r.Post("/{id}/transition", s.handleTaskTransition(store))
		r.Post("/{id}/comment", s.handleTaskComment(store))
		r.Get("/{id}/events", s.handleTaskEvents(store))
		r.Post("/{id}/dependencies", s.handleTaskAddDep(store))
		r.Get("/{id}/dependencies", s.handleTaskGetDeps(store))
	})
}

func (s *Server) handleTaskCreate(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title       string                 `json:"title"`
			Description string                 `json:"description"`
			Priority    string                 `json:"priority"`
			Assignee    string                 `json:"assignee"`
			ParentID    string                 `json:"parent_id"`
			DueDate     string                 `json:"due_date"`
			Tags        []string               `json:"tags"`
			Metadata    map[string]interface{} `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Title == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "title is required")
			return
		}
		t, err := store.Create(req.Title, req.Description, req.Priority, req.Assignee,
			GetAgentID(r), req.ParentID, req.DueDate, req.Tags, req.Metadata)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, t)
	}
}

func (s *Server) handleTaskList(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, err := store.List(tasks.ListOptions{
			Status:   q.Get("status"),
			Assignee: q.Get("assignee"),
			Priority: q.Get("priority"),
			Tag:      q.Get("tag"),
			ParentID: q.Get("parent_id"),
			Cursor:   q.Get("cursor"),
			Limit:    limit,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleTaskGet(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := store.Get(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Task not found")
			return
		}
		events, _ := store.GetEvents(id)
		deps, _ := store.GetDependencies(id)
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"task":         t,
			"events":       events,
			"dependencies": deps,
		})
	}
}

func (s *Server) handleTaskUpdate(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		t, err := store.Update(id, GetAgentID(r), updates)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Task not found")
			return
		}
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleTaskTransition(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		t, err := store.Transition(id, GetAgentID(r), req.Status)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Task not found")
			return
		}
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleTaskComment(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Comment string `json:"comment"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.AddComment(id, GetAgentID(r), req.Comment); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "commented"})
	}
}

func (s *Server) handleTaskEvents(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		events, err := store.GetEvents(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, events)
	}
}

func (s *Server) handleTaskAddDep(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			DependsOn string `json:"depends_on"`
			DepType   string `json:"dep_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.AddDependency(id, req.DependsOn, req.DepType); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "added"})
	}
}

func (s *Server) handleTaskGetDeps(store *tasks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		deps, err := store.GetDependencies(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, deps)
	}
}
