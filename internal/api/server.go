package api

import (
	"encoding/json"
	"net/http"

	"github.com/garett/aiprod/internal/auth"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	Router      *chi.Mux
	AuthStore   *auth.Store
	NoAuth      bool
}

func NewServer(authStore *auth.Store, noAuth bool) *Server {
	s := &Server{
		Router:    chi.NewRouter(),
		AuthStore: authStore,
		NoAuth:    noAuth,
	}

	s.Router.Use(chimw.Logger)
	s.Router.Use(chimw.Recoverer)
	s.Router.Use(chimw.RealIP)

	s.Router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
	})

	return s
}

// SetupV1Routes sets up the /api/v1 route group with all module routes.
// Called after all stores are initialized.
func (s *Server) SetupV1Routes(setup func(r chi.Router)) {
	s.Router.Route("/api/v1", func(r chi.Router) {
		r.Use(s.AuthMiddleware)
		s.registerAuthRoutes(r)
		setup(r)
	})
}

// Response is the standard API response envelope.
type Response struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error *APIError   `json:"error,omitempty"`
}

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{OK: true, Data: data})
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(Response{OK: false, Error: &APIError{Code: code, Message: message}})
}
