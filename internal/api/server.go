package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"localflow/internal/domain"
	"localflow/internal/queue"
)

type Server struct { r *chi.Mux; repo queue.Repository }

func NewServer(repo queue.Repository) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	s := &Server{r: r, repo: repo}
	r.Get("/health", s.health)
	r.Get("/metrics", s.metrics)
	r.Post("/api/tasks", s.submitTask)
	r.Get("/api/tasks/{id}", s.getTask)
	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK); w.Write([]byte("ok"))
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("content-type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("localflow_up 1\n"))
}

type submitReq struct {
	Type           string          `json:"type"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int             `json:"priority"`
	MaxAttempts    int             `json:"max_attempts"`
	IdempotencyKey *string         `json:"idempotency_key"`
}

type submitResp struct { ID string `json:"id"` }

func (s *Server) submitTask(w http.ResponseWriter, r *http.Request) {
	var req submitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, err.Error(), 400); return }
	if req.Type == "" { http.Error(w, "type is required", 400); return }
	id, err := s.repo.Enqueue(r.Context(), domain.Task{
		Type: req.Type, Payload: req.Payload, Priority: req.Priority,
		MaxAttempts: req.MaxAttempts, IdempotencyKey: req.IdempotencyKey,
		VisibilityTimeout: 60,
	})
	if err != nil { http.Error(w, err.Error(), 500); return }
	writeJSON(w, http.StatusAccepted, submitResp{ID: id})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.repo.Get(r.Context(), id)
	if err != nil { http.Error(w, "not found", 404); return }
	writeJSON(w, 200, map[string]any{
		"id": t.ID,
		"type": t.Type,
		"state": t.State,
		"attempts": t.Attempts,
		"max_attempts": t.MaxAttempts,
		"priority": t.Priority,
		"next_run_at": t.NextRunAt.Format(time.RFC3339),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}