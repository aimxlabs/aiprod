package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/garett/aiprod/internal/search"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterSearchRoutes(r chi.Router, svc *search.Service) {
	r.Get("/search", s.handleSearch(svc))
}

func (s *Server) handleSearch(svc *search.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "q parameter required")
			return
		}
		scopeStr := r.URL.Query().Get("scope")
		var scopes []string
		if scopeStr != "" {
			scopes = strings.Split(scopeStr, ",")
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))

		results, err := svc.Search(q, scopes, limit)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, results)
	}
}
