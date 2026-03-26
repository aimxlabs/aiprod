package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/storage"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterFilesRoutes(r chi.Router, store *storage.Store) {
	r.Route("/files", func(r chi.Router) {
		r.Post("/", s.handleFileUpload(store))
		r.Get("/", s.handleFileList(store))
		r.Get("/{id}", s.handleFileDownload(store))
		r.Get("/{id}/meta", s.handleFileGetMeta(store))
		r.Patch("/{id}/meta", s.handleFileUpdateMeta(store))
		r.Delete("/{id}", s.handleFileDelete(store))
	})
}

func (s *Server) handleFileUpload(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Support both multipart and raw body upload
		var name, mimeType string
		var reader io.Reader

		if r.Header.Get("Content-Type") != "" && r.Header.Get("Content-Type") != "application/octet-stream" &&
			r.Header.Get("Content-Type") != "application/json" {
			// Multipart upload
			if err := r.ParseMultipartForm(100 << 20); err != nil {
				WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Failed to parse multipart form")
				return
			}
			file, header, err := r.FormFile("file")
			if err != nil {
				WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Missing 'file' field")
				return
			}
			defer file.Close()
			name = header.Filename
			mimeType = header.Header.Get("Content-Type")
			reader = file
		} else {
			// Raw body upload
			name = r.URL.Query().Get("name")
			if name == "" {
				name = "unnamed"
			}
			mimeType = r.Header.Get("Content-Type")
			reader = r.Body
		}

		tagsParam := r.URL.Query().Get("tags")
		var tags []string
		if tagsParam != "" {
			json.Unmarshal([]byte(tagsParam), &tags)
		}

		f, err := store.Put(name, mimeType, GetAgentID(r), tags, nil, reader)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, f)
	}
}

func (s *Server) handleFileList(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		files, err := store.List(storage.ListOptions{
			Tag:    q.Get("tag"),
			Cursor: q.Get("cursor"),
			Limit:  limit,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, files)
	}
}

func (s *Server) handleFileDownload(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		reader, f, err := store.Open(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if reader == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "File not found")
			return
		}
		defer reader.Close()

		if f.MimeType != "" {
			w.Header().Set("Content-Type", f.MimeType)
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\""+f.Name+"\"")
		io.Copy(w, reader)
	}
}

func (s *Server) handleFileGetMeta(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		f, err := store.Get(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if f == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "File not found")
			return
		}
		WriteJSON(w, http.StatusOK, f)
	}
}

func (s *Server) handleFileUpdateMeta(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Tags     []string               `json:"tags"`
			Metadata map[string]interface{} `json:"metadata"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON body")
			return
		}
		if err := store.UpdateMeta(id, req.Tags, req.Metadata); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

func (s *Server) handleFileDelete(store *storage.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := store.Delete(id); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
