package api

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/pprof"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"localflow/internal/domain"
	"localflow/internal/queue"
	"localflow/internal/scheduler"
)

type Server struct {
	r         *chi.Mux
	repo      queue.Repository
	templates *template.Template
}

func NewServer(repo queue.Repository) http.Handler {
	return NewServerWithDebug(repo, false)
}

func NewServerWithDebug(repo queue.Repository, enableDebug bool) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)

	// Load templates
	templates := template.Must(template.ParseGlob("templates/*.html"))

	s := &Server{r: r, repo: repo, templates: templates}

	// API routes
	r.Get("/health", s.health)
	r.Get("/metrics", s.metrics)
	r.Post("/api/tasks", s.submitTask)
	r.Get("/api/tasks/{id}", s.getTask)
	r.Post("/api/schedules", s.createSchedule)
	r.Get("/api/schedules", s.listSchedules)
	r.Get("/api/schedules/{id}", s.getSchedule)
	r.Put("/api/schedules/{id}", s.updateSchedule)
	r.Delete("/api/schedules/{id}", s.deleteSchedule)

	// Dashboard routes
	r.Get("/", s.dashboard)
	r.Get("/dashboard", s.dashboard)
	r.Get("/dashboard/tasks", s.dashboardTasks)
	r.Get("/dashboard/schedules", s.dashboardSchedules)
	r.Post("/dashboard/tasks", s.dashboardSubmitTask)
	r.Post("/dashboard/schedules", s.dashboardCreateSchedule)
	r.Delete("/dashboard/schedules/{id}", s.dashboardDeleteSchedule)

	// Debug routes (pprof)
	if enableDebug {
		r.HandleFunc("/debug/pprof/", pprof.Index)
		r.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		r.HandleFunc("/debug/pprof/profile", pprof.Profile)
		r.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/debug/pprof/trace", pprof.Trace)
		r.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
		r.Handle("/debug/pprof/heap", pprof.Handler("heap"))
		r.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
		r.Handle("/debug/pprof/block", pprof.Handler("block"))
	}

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
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

type submitResp struct {
	ID string `json:"id"`
}

func (s *Server) submitTask(w http.ResponseWriter, r *http.Request) {
	var req submitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.Type == "" {
		http.Error(w, "type is required", 400)
		return
	}
	id, err := s.repo.Enqueue(r.Context(), domain.Task{
		Type: req.Type, Payload: req.Payload, Priority: req.Priority,
		MaxAttempts: req.MaxAttempts, IdempotencyKey: req.IdempotencyKey,
		VisibilityTimeout: 60,
	})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, http.StatusAccepted, submitResp{ID: id})
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.repo.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":           t.ID,
		"type":         t.Type,
		"state":        t.State,
		"attempts":     t.Attempts,
		"max_attempts": t.MaxAttempts,
		"priority":     t.Priority,
		"next_run_at":  t.NextRunAt.Format(time.RFC3339),
	})
}

type createScheduleReq struct {
	Name        string          `json:"name"`
	CronExpr    string          `json:"cron_expr"`
	TaskType    string          `json:"task_type"`
	Payload     json.RawMessage `json:"payload"`
	Priority    int             `json:"priority"`
	MaxAttempts int             `json:"max_attempts"`
	Enabled     bool            `json:"enabled"`
}

type createScheduleResp struct {
	ID string `json:"id"`
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	var req createScheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", 400)
		return
	}
	if req.CronExpr == "" {
		http.Error(w, "cron_expr is required", 400)
		return
	}
	if req.TaskType == "" {
		http.Error(w, "task_type is required", 400)
		return
	}

	// Validate cron expression
	if err := scheduler.ValidateCronExpression(req.CronExpr); err != nil {
		http.Error(w, "invalid cron expression: "+err.Error(), 400)
		return
	}

	// Calculate next run time
	nextRun, err := scheduler.NextRunTime(req.CronExpr, time.Now())
	if err != nil {
		http.Error(w, "failed to calculate next run time: "+err.Error(), 400)
		return
	}

	schedule := domain.Schedule{
		Name:        req.Name,
		CronExpr:    req.CronExpr,
		TaskType:    req.TaskType,
		Payload:     req.Payload,
		Priority:    req.Priority,
		MaxAttempts: req.MaxAttempts,
		Enabled:     req.Enabled,
		NextRun:     nextRun,
	}

	id, err := s.repo.CreateSchedule(r.Context(), schedule)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, http.StatusCreated, createScheduleResp{ID: id})
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := s.repo.ListSchedules(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, schedules)
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	schedule, err := s.repo.GetSchedule(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	writeJSON(w, 200, schedule)
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Get existing schedule
	schedule, err := s.repo.GetSchedule(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}

	var req createScheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Update fields
	if req.Name != "" {
		schedule.Name = req.Name
	}
	if req.CronExpr != "" {
		if err := scheduler.ValidateCronExpression(req.CronExpr); err != nil {
			http.Error(w, "invalid cron expression: "+err.Error(), 400)
			return
		}
		schedule.CronExpr = req.CronExpr
		// Recalculate next run time
		nextRun, err := scheduler.NextRunTime(req.CronExpr, time.Now())
		if err != nil {
			http.Error(w, "failed to calculate next run time: "+err.Error(), 400)
			return
		}
		schedule.NextRun = nextRun
	}
	if req.TaskType != "" {
		schedule.TaskType = req.TaskType
	}
	if req.Payload != nil {
		schedule.Payload = req.Payload
	}
	if req.Priority > 0 {
		schedule.Priority = req.Priority
	}
	if req.MaxAttempts > 0 {
		schedule.MaxAttempts = req.MaxAttempts
	}
	schedule.Enabled = req.Enabled

	if err := s.repo.UpdateSchedule(r.Context(), schedule); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, 200, schedule)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.repo.DeleteSchedule(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Dashboard handlers
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if err := s.templates.ExecuteTemplate(w, "dashboard.html", nil); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) dashboardTasks(w http.ResponseWriter, r *http.Request) {
	// Get recent tasks (limit 50)
	tasks, err := s.repo.ListRecentTasks(r.Context(), 50)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if len(tasks) > 0 {
		w.Write([]byte(`<table class="table"><thead><tr><th>ID</th><th>Type</th><th>State</th><th>Attempts</th><th>Priority</th><th>Created</th></tr></thead><tbody>`))
		if err := s.templates.ExecuteTemplate(w, "tasks.html", tasks); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte(`</tbody></table>`))
	} else {
		w.Write([]byte(`<p>No tasks found</p>`))
	}
}

func (s *Server) dashboardSchedules(w http.ResponseWriter, r *http.Request) {
	schedules, err := s.repo.ListSchedules(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	if len(schedules) > 0 {
		w.Write([]byte(`<table class="table"><thead><tr><th>Name</th><th>Cron</th><th>Type</th><th>Enabled</th><th>Last Run</th><th>Next Run</th><th>Actions</th></tr></thead><tbody>`))
		if err := s.templates.ExecuteTemplate(w, "schedules.html", schedules); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Write([]byte(`</tbody></table>`))
	} else {
		w.Write([]byte(`<p>No schedules found</p>`))
	}
}

func (s *Server) dashboardSubmitTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	taskType := r.FormValue("type")
	payload := r.FormValue("payload")
	priorityStr := r.FormValue("priority")
	maxAttemptsStr := r.FormValue("max_attempts")
	idempotencyKey := r.FormValue("idempotency_key")

	if taskType == "" || payload == "" {
		http.Error(w, "type and payload are required", 400)
		return
	}

	priority, _ := strconv.Atoi(priorityStr)
	if priority <= 0 {
		priority = 5
	}

	maxAttempts, _ := strconv.Atoi(maxAttemptsStr)
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	task := domain.Task{
		Type:        taskType,
		Payload:     []byte(payload),
		Priority:    priority,
		MaxAttempts: maxAttempts,
	}

	if idempotencyKey != "" {
		task.IdempotencyKey = &idempotencyKey
	}

	id, err := s.repo.Enqueue(r.Context(), task)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<div class="card" style="background: #d4edda; border: 1px solid #c3e6cb;"><p>✅ Task submitted successfully! ID: ` + id + `</p></div>`))
}

func (s *Server) dashboardCreateSchedule(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	name := r.FormValue("name")
	cronExpr := r.FormValue("cron_expr")
	taskType := r.FormValue("task_type")
	payload := r.FormValue("payload")
	priorityStr := r.FormValue("priority")
	maxAttemptsStr := r.FormValue("max_attempts")
	enabled := r.FormValue("enabled") == "on"

	if name == "" || cronExpr == "" || taskType == "" {
		http.Error(w, "name, cron_expr, and task_type are required", 400)
		return
	}

	if err := scheduler.ValidateCronExpression(cronExpr); err != nil {
		http.Error(w, "invalid cron expression: "+err.Error(), 400)
		return
	}

	nextRun, err := scheduler.NextRunTime(cronExpr, time.Now())
	if err != nil {
		http.Error(w, "failed to calculate next run time: "+err.Error(), 400)
		return
	}

	priority, _ := strconv.Atoi(priorityStr)
	if priority <= 0 {
		priority = 5
	}

	maxAttempts, _ := strconv.Atoi(maxAttemptsStr)
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	schedule := domain.Schedule{
		Name:        name,
		CronExpr:    cronExpr,
		TaskType:    taskType,
		Payload:     []byte(payload),
		Priority:    priority,
		MaxAttempts: maxAttempts,
		Enabled:     enabled,
		NextRun:     nextRun,
	}

	_, err = s.repo.CreateSchedule(r.Context(), schedule)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Return updated schedules list
	s.dashboardSchedules(w, r)
}

func (s *Server) dashboardDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.repo.DeleteSchedule(r.Context(), id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Return updated schedules list
	s.dashboardSchedules(w, r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
