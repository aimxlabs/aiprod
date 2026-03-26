package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/knowledge"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterKnowledgeRoutes(r chi.Router, store *knowledge.Store) {
	r.Route("/facts", func(r chi.Router) {
		r.Post("/", s.handleFactCreate(store))
		r.Get("/", s.handleFactList(store))
		r.Get("/{id}", s.handleFactGet(store))
		r.Patch("/{id}", s.handleFactUpdate(store))
		r.Delete("/{id}", s.handleFactDelete(store))
	})
	r.Route("/entities", func(r chi.Router) {
		r.Post("/", s.handleEntityCreate(store))
		r.Get("/", s.handleEntityList(store))
		r.Get("/{id}", s.handleEntityGet(store))
		r.Patch("/{id}", s.handleEntityUpdate(store))
		r.Delete("/{id}", s.handleEntityDelete(store))
		r.Post("/{id}/relations", s.handleRelationAdd(store))
		r.Get("/{id}/relations", s.handleRelationList(store))
		r.Delete("/relations/{relId}", s.handleRelationRemove(store))
	})
	r.Route("/schemas", func(r chi.Router) {
		r.Post("/", s.handleSchemaInfer(store))
		r.Get("/", s.handleSchemaList(store))
		r.Get("/{sourceType}/{sourceId}", s.handleSchemaGet(store))
	})
}

func (s *Server) handleFactCreate(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var f knowledge.Fact
		if err := json.NewDecoder(r.Body).Decode(&f); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if f.AgentID == "" { f.AgentID = GetAgentID(r) }
		if f.Subject == "" || f.Predicate == "" || f.Object == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "subject, predicate, and object are required")
			return
		}
		result, err := store.CreateFact(&f)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleFactList(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := knowledge.FactListOpts{
			AgentID:   r.URL.Query().Get("agent_id"),
			Subject:   r.URL.Query().Get("subject"),
			Predicate: r.URL.Query().Get("predicate"),
			Query:     r.URL.Query().Get("q"),
		}
		if v := r.URL.Query().Get("min_confidence"); v != "" { opts.MinConf, _ = strconv.ParseFloat(v, 64) }
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListFacts(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleFactGet(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := store.GetFact(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if f == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Fact not found")
			return
		}
		WriteJSON(w, http.StatusOK, f)
	}
}

func (s *Server) handleFactUpdate(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdateFact(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		f, _ := store.GetFact(id)
		WriteJSON(w, http.StatusOK, f)
	}
}

func (s *Server) handleFactDelete(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteFact(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleEntityCreate(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var e knowledge.Entity
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if e.Name == "" || e.EntityType == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name and entity_type are required")
			return
		}
		result, err := store.CreateEntity(&e)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleEntityList(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListEntities(r.URL.Query().Get("type"), r.URL.Query().Get("q"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleEntityGet(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := store.GetEntity(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if e == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Entity not found")
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}

func (s *Server) handleEntityUpdate(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var updates map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		id := chi.URLParam(r, "id")
		if err := store.UpdateEntity(id, updates); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		e, _ := store.GetEntity(id)
		WriteJSON(w, http.StatusOK, e)
	}
}

func (s *Server) handleEntityDelete(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteEntity(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleRelationAdd(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rel knowledge.EntityRelation
		if err := json.NewDecoder(r.Body).Decode(&rel); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		rel.FromEntity = chi.URLParam(r, "id")
		if rel.ToEntity == "" || rel.RelationType == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "to_entity and relation_type are required")
			return
		}
		result, err := store.AddRelation(&rel)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleRelationList(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		direction := r.URL.Query().Get("direction")
		result, err := store.GetRelations(chi.URLParam(r, "id"), direction)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleRelationRemove(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.RemoveRelation(chi.URLParam(r, "relId")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "relId")})
	}
}

func (s *Server) handleSchemaInfer(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var si knowledge.SchemaInference
		if err := json.NewDecoder(r.Body).Decode(&si); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if si.SourceType == "" || si.InferredSchema == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "source_type and inferred_schema are required")
			return
		}
		result, err := store.SaveInference(&si)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleSchemaList(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.ListInferences(r.URL.Query().Get("source_type"), limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleSchemaGet(store *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		si, err := store.GetInference(chi.URLParam(r, "sourceType"), chi.URLParam(r, "sourceId"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if si == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Schema inference not found")
			return
		}
		WriteJSON(w, http.StatusOK, si)
	}
}
