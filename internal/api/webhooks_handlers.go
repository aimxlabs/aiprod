package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/webhooks"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterWebhooksRoutes(r chi.Router, store *webhooks.Store) {
	r.Route("/webhooks/subscriptions", func(r chi.Router) {
		r.Post("/", s.handleWebhookSubCreate(store))
		r.Get("/", s.handleWebhookSubList(store))
		r.Get("/{id}", s.handleWebhookSubGet(store))
		r.Delete("/{id}", s.handleWebhookSubDelete(store))
		r.Post("/{id}/activate", s.handleWebhookSubActivate(store))
		r.Post("/{id}/deactivate", s.handleWebhookSubDeactivate(store))
		r.Post("/{id}/listen", s.handleWebhookListen(store))
		r.Post("/{id}/stop", s.handleWebhookStop(store))
		r.Post("/{id}/poll", s.handleWebhookPoll(store))
	})
	r.Route("/webhooks/events", func(r chi.Router) {
		r.Get("/", s.handleWebhookEventList(store))
		r.Get("/{id}", s.handleWebhookEventGet(store))
	})
	r.Get("/webhooks/status", s.handleWebhookStatus(store))
}

func (s *Server) handleWebhookSubCreate(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sub webhooks.Subscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if sub.ChannelID == "" || sub.ServerURL == "" || sub.Token == "" {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "channel_id, server_url, and token are required")
			return
		}
		result, err := store.CreateSubscription(&sub)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, result)
	}
}

func (s *Server) handleWebhookSubList(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := store.ListSubscriptions()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleWebhookSubGet(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sub, err := store.GetSubscription(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if sub == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Subscription not found")
			return
		}
		WriteJSON(w, http.StatusOK, sub)
	}
}

func (s *Server) handleWebhookSubDelete(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.DeleteSubscription(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deleted": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleWebhookSubActivate(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.SetActive(chi.URLParam(r, "id"), true); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"activated": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleWebhookSubDeactivate(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.SetActive(chi.URLParam(r, "id"), false); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"deactivated": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleWebhookListen(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.StartListener(chi.URLParam(r, "id")); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"listening": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleWebhookStop(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store.StopListener(chi.URLParam(r, "id"))
		WriteJSON(w, http.StatusOK, map[string]string{"stopped": chi.URLParam(r, "id")})
	}
}

func (s *Server) handleWebhookPoll(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		events, err := store.Poll(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, events)
	}
}

func (s *Server) handleWebhookEventList(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		opts := webhooks.EventListOpts{
			ChannelID: r.URL.Query().Get("channel_id"),
		}
		if v := r.URL.Query().Get("limit"); v != "" { opts.Limit, _ = strconv.Atoi(v) }
		result, err := store.ListEvents(opts)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, result)
	}
}

func (s *Server) handleWebhookEventGet(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		evt, err := store.GetEvent(chi.URLParam(r, "id"))
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if evt == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Event not found")
			return
		}
		WriteJSON(w, http.StatusOK, evt)
	}
}

func (s *Server) handleWebhookStatus(store *webhooks.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, store.ListenerStatus())
	}
}
