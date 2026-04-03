package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"

	"github.com/garett/aiprod/internal/email"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterEmailRoutes(r chi.Router, store *email.Store, sender email.Sender) {
	r.Route("/email", func(r chi.Router) {
		r.Post("/send", s.handleEmailSend(sender))
		r.Get("/messages", s.handleEmailList(store))
		r.Get("/messages/{id}", s.handleEmailGet(store))
		r.Patch("/messages/{id}", s.handleEmailUpdate(store))
		r.Delete("/messages/{id}", s.handleEmailDelete(store))
		r.Get("/threads/{id}", s.handleEmailThread(store))
		r.Get("/search", s.handleEmailSearch(store))
	})
}

func (s *Server) handleEmailSend(sender email.Sender) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			From    string   `json:"from"`
			To      []string `json:"to"`
			Cc      []string `json:"cc"`
			Subject string   `json:"subject"`
			Body    string   `json:"body"`
			HTML    string   `json:"html"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.From == "" {
			req.From = os.Getenv("AIPROD_MAILR_ADDRESS")
		}
		if len(req.To) == 0 {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "to is required")
			return
		}
		msg, err := sender.Send(req.From, req.To, req.Cc, req.Subject, req.Body, req.HTML)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, msg)
	}
}

func (s *Server) handleEmailList(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		msgs, err := store.List(email.ListOptions{
			Label:     q.Get("label"),
			Direction: q.Get("direction"),
			ThreadID:  q.Get("thread_id"),
			Cursor:    q.Get("cursor"),
			Limit:     limit,
		})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, msgs)
	}
}

func (s *Server) handleEmailGet(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		msg, err := store.Get(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if msg == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Message not found")
			return
		}
		WriteJSON(w, http.StatusOK, msg)
	}
}

func (s *Server) handleEmailUpdate(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			AddLabels    []string `json:"add_labels"`
			RemoveLabels []string `json:"remove_labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		for _, l := range req.AddLabels {
			store.AddLabel(id, l)
		}
		for _, l := range req.RemoveLabels {
			store.RemoveLabel(id, l)
		}
		msg, _ := store.Get(id)
		WriteJSON(w, http.StatusOK, msg)
	}
}

func (s *Server) handleEmailDelete(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		store.AddLabel(id, "trash")
		store.RemoveLabel(id, "inbox")
		WriteJSON(w, http.StatusOK, map[string]string{"status": "trashed"})
	}
}

func (s *Server) handleEmailThread(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		threadID := chi.URLParam(r, "id")
		msgs, err := store.GetThread(threadID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, msgs)
	}
}

func (s *Server) handleEmailSearch(store *email.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "q parameter required")
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		msgs, err := store.Search(q, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, msgs)
	}
}
