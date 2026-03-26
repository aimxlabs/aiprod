package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/docs"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterDocsRoutes(r chi.Router, store *docs.Store) {
	r.Route("/docs", func(r chi.Router) {
		r.Post("/", s.handleDocCreate(store))
		r.Get("/", s.handleDocList(store))
		r.Get("/search", s.handleDocSearch(store))
		r.Get("/{id}", s.handleDocRead(store))
		r.Put("/{id}", s.handleDocUpdate(store))
		r.Get("/{id}/versions", s.handleDocVersions(store))
		r.Get("/{id}/versions/{version}", s.handleDocReadVersion(store))
		r.Delete("/{id}", s.handleDocDelete(store))
	})
}

func (s *Server) handleDocCreate(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		var title, content string
		var tags []string

		if ct == "application/json" || ct == "" {
			var req struct {
				Title   string   `json:"title"`
				Content string   `json:"content"`
				Tags    []string `json:"tags"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
				return
			}
			title = req.Title
			content = req.Content
			tags = req.Tags
		} else {
			// Raw markdown body
			title = r.URL.Query().Get("title")
			body, _ := io.ReadAll(r.Body)
			content = string(body)
		}

		if title == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "title is required")
			return
		}

		doc, err := store.Create(title, GetAgentID(r), tags, content)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, doc)
	}
}

func (s *Server) handleDocList(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		result, err := store.List(docs.ListOptions{
			Tag:    q.Get("tag"),
			Author: q.Get("author"),
			Cursor: q.Get("cursor"),
			Limit:  limit,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleDocRead(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		format := r.URL.Query().Get("format")

		if format == "raw" || r.Header.Get("Accept") == "text/markdown" {
			content, err := store.ReadContent(id)
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
				return
			}
			if content == "" {
				WriteError(w, http.StatusNotFound, "NOT_FOUND", "Document not found")
				return
			}
			w.Header().Set("Content-Type", "text/markdown")
			w.Write([]byte(content))
			return
		}

		doc, err := store.Get(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if doc == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Document not found")
			return
		}
		content, _ := store.ReadContent(id)
		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"document": doc,
			"content":  content,
		})
	}
}

func (s *Server) handleDocUpdate(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Content string `json:"content"`
			Message string `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		doc, err := store.Update(id, GetAgentID(r), req.Message, req.Content)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if doc == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Document not found")
			return
		}
		WriteJSON(w, http.StatusOK, doc)
	}
}

func (s *Server) handleDocVersions(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		versions, err := store.ListVersions(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, versions)
	}
}

func (s *Server) handleDocReadVersion(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		v, err := strconv.Atoi(chi.URLParam(r, "version"))
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid version number")
			return
		}
		content, err := store.ReadVersion(id, v)
		if err != nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(content))
	}
}

func (s *Server) handleDocSearch(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "q parameter required")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		result, err := store.Search(q, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleDocDelete(store *docs.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := store.Delete(id); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
