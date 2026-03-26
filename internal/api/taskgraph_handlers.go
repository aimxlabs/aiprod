package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/taskgraph"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterTaskGraphRoutes(r chi.Router, store *taskgraph.Store) {
	r.Route("/graphs", func(r chi.Router) {
		r.Post("/", s.handleGraphCreate(store))
		r.Get("/", s.handleGraphList(store))
		r.Get("/{id}", s.handleGraphGet(store))
		r.Patch("/{id}", s.handleGraphUpdate(store))
		r.Delete("/{id}", s.handleGraphDelete(store))
		r.Post("/{id}/nodes", s.handleNodeAdd(store))
		r.Get("/{id}/nodes", s.handleNodeList(store))
		r.Patch("/nodes/{nodeId}", s.handleNodeUpdate(store))
		r.Post("/{id}/edges", s.handleEdgeAdd(store))
		r.Get("/{id}/edges", s.handleEdgeList(store))
		r.Delete("/edges/{edgeId}", s.handleEdgeRemove(store))
		r.Get("/{id}/ready", s.handleReadyNodes(store))
	})
}

func (s *Server) handleGraphCreate(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var g taskgraph.Graph
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if g.AgentID == "" { g.AgentID = GetAgentID(r) }
		if g.Name == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
			return
		}
		result, err := store.CreateGraph(&g)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleGraphList(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListGraphs(r.URL.Query().Get("agent_id"), r.URL.Query().Get("status"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleGraphGet(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		includeNodes := r.URL.Query().Get("nodes") != "false"
		g, err := store.GetGraph(chi.URLParam(r, "id"), includeNodes)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if g == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Graph not found")
			return
		}
		WriteJSON(w, http.StatusOK, g)
	}
}

func (s *Server) handleGraphUpdate(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdateGraph(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		g, _ := store.GetGraph(id, false)
		WriteJSON(w, http.StatusOK, g)
	}
}

func (s *Server) handleGraphDelete(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteGraph(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleNodeAdd(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var n taskgraph.Node
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		n.GraphID = chi.URLParam(r, "id")
		result, err := store.AddNode(&n)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleNodeList(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.GetNodes(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleNodeUpdate(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.UpdateNode(chi.URLParam(r, "nodeId"), updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"updated": chi.URLParam(r, "nodeId")})
	}
}

func (s *Server) handleEdgeAdd(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var e taskgraph.Edge
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		e.GraphID = chi.URLParam(r, "id")
		result, err := store.AddEdge(&e)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleEdgeList(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.GetEdges(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleEdgeRemove(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.RemoveEdge(chi.URLParam(r, "edgeId")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "edgeId")})
	}
}

func (s *Server) handleReadyNodes(store *taskgraph.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ReadyNodes(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}
