package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"localflow/internal/domain"
)

var ErrEmpty = errors.New("no tasks ready")

// EnsureSchema creates tables if they don't exist.
func EnsureSchema(db *sql.DB) error {
	schema := `
PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  type TEXT NOT NULL,
  payload BLOB NOT NULL,
  priority INTEGER NOT NULL DEFAULT 5,
  state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed','canceled')) DEFAULT 'queued',
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  next_run_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  visibility_timeout INTEGER NOT NULL DEFAULT 60,
  idempotency_key TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_tasks_next_run ON tasks(state, next_run_at, priority DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_idem ON tasks(idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS task_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME,
  success INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);
CREATE TABLE IF NOT EXISTS schedules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  cron_expr TEXT NOT NULL,
  task_type TEXT NOT NULL,
  payload BLOB NOT NULL,
  priority INTEGER NOT NULL DEFAULT 5,
  max_attempts INTEGER NOT NULL DEFAULT 5,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run DATETIME,
  next_run DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_schedules_next_run ON schedules(enabled, next_run);
`
	_, err := db.Exec(schema)
	return err
}

type Repository interface {
	Enqueue(ctx context.Context, t domain.Task) (string, error)
	LeaseNext(ctx context.Context, now time.Time) (domain.Task, Lease, error)
	Retry(ctx context.Context, id, err string, delay time.Duration) error
	Succeed(ctx context.Context, id string) error
	Fail(ctx context.Context, id, err string, delay time.Duration) error
	RecoverStale(ctx context.Context, now time.Time) (int, error)
	Get(ctx context.Context, id string) (domain.Task, error)
	ListRecentTasks(ctx context.Context, limit int) ([]domain.Task, error)

	// Schedule operations
	CreateSchedule(ctx context.Context, s domain.Schedule) (string, error)
	GetSchedule(ctx context.Context, id string) (domain.Schedule, error)
	ListSchedules(ctx context.Context) ([]domain.Schedule, error)
	UpdateSchedule(ctx context.Context, s domain.Schedule) error
	DeleteSchedule(ctx context.Context, id string) error
	GetDueSchedules(ctx context.Context, now time.Time) ([]domain.Schedule, error)
	UpdateScheduleLastRun(ctx context.Context, id string, lastRun, nextRun time.Time) error
}

type sqliteRepo struct{ db *sql.DB }

func NewSQLiteRepo(db *sql.DB) Repository { return &sqliteRepo{db: db} }

// DB returns the underlying database connection (for dashboard queries)
func (r *sqliteRepo) DB() *sql.DB { return r.db }

type Lease struct{ Until time.Time }

func (r *sqliteRepo) Enqueue(ctx context.Context, t domain.Task) (string, error) {
	id := t.ID
	if id == "" {
		id = "tsk_" + uuid.NewString()
	}
	if t.Priority == 0 {
		t.Priority = 5
	}
	if t.MaxAttempts == 0 {
		t.MaxAttempts = 5
	}
	if t.VisibilityTimeout == 0 {
		t.VisibilityTimeout = 60
	}

	// Check for existing task with same idempotency key
	if t.IdempotencyKey != nil {
		row := r.db.QueryRowContext(ctx, "SELECT id FROM tasks WHERE idempotency_key = ?", *t.IdempotencyKey)
		var existingID string
		if err := row.Scan(&existingID); err == nil {
			return existingID, nil // Return existing task ID
		}
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO tasks (id,type,payload,priority,state,attempts,max_attempts,next_run_at,visibility_timeout,idempotency_key,created_at,updated_at)
VALUES (?,?,?,?, 'queued',0,?, CURRENT_TIMESTAMP, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`, id, t.Type, t.Payload, t.Priority, t.MaxAttempts, t.VisibilityTimeout, t.IdempotencyKey)
	return id, err
}

func (r *sqliteRepo) LeaseNext(ctx context.Context, now time.Time) (domain.Task, Lease, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return domain.Task{}, Lease{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	row := tx.QueryRowContext(ctx, `
SELECT id,type,payload,priority,attempts,max_attempts,state,next_run_at,visibility_timeout,idempotency_key,created_at,updated_at
FROM tasks
WHERE state='queued' AND next_run_at <= ?
ORDER BY priority DESC, created_at ASC
LIMIT 1
`, now)
	var t domain.Task
	var idem sql.NullString
	err = row.Scan(&t.ID, &t.Type, &t.Payload, &t.Priority, &t.Attempts, &t.MaxAttempts, &t.State, &t.NextRunAt, &t.VisibilityTimeout, &idem, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return domain.Task{}, Lease{}, tx.Rollback()
	}
	if err != nil {
		return domain.Task{}, Lease{}, err
	}
	if idem.Valid {
		s := idem.String
		t.IdempotencyKey = &s
	}

	leaseUntil := now.Add(time.Duration(t.VisibilityTimeout) * time.Second)
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET state='running', updated_at=CURRENT_TIMESTAMP WHERE id=?`, t.ID)
	if err != nil {
		return domain.Task{}, Lease{}, err
	}

	if err = tx.Commit(); err != nil {
		return domain.Task{}, Lease{}, err
	}
	return t, Lease{Until: leaseUntil}, nil
}

func (r *sqliteRepo) Retry(ctx context.Context, id, errStr string, delay time.Duration) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO task_attempts(task_id, success, error, finished_at) VALUES (?,0,?,CURRENT_TIMESTAMP);
UPDATE tasks
SET attempts = attempts + 1,
    state = CASE WHEN attempts + 1 >= max_attempts THEN 'failed' ELSE 'queued' END,
    next_run_at = datetime(CURRENT_TIMESTAMP, ?),
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;
`, id, errStr, fmt.Sprintf("+%d seconds", int(delay.Seconds())), id)
	return err
}

func (r *sqliteRepo) Succeed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO task_attempts(task_id, success, error, finished_at) VALUES (?,1,'',CURRENT_TIMESTAMP);
UPDATE tasks SET state='succeeded', updated_at=CURRENT_TIMESTAMP WHERE id=?;`, id, id)
	return err
}

func (r *sqliteRepo) Fail(ctx context.Context, id, errStr string, delay time.Duration) error {
	// Hard fail: move to failed and stop
	_, err := r.db.ExecContext(ctx, `
INSERT INTO task_attempts(task_id, success, error, finished_at) VALUES (?,0,?,CURRENT_TIMESTAMP);
UPDATE tasks SET state='failed', updated_at=CURRENT_TIMESTAMP WHERE id=?;`, id, errStr, id)
	return err
}

func (r *sqliteRepo) RecoverStale(ctx context.Context, now time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx, `
UPDATE tasks
SET state='queued', next_run_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP
WHERE state='running' AND strftime('%s','now') - strftime('%s',updated_at) > visibility_timeout;`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *sqliteRepo) Get(ctx context.Context, id string) (domain.Task, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id,type,payload,priority,attempts,max_attempts,state,next_run_at,visibility_timeout,idempotency_key,created_at,updated_at
FROM tasks WHERE id=?`, id)
	var t domain.Task
	var idem sql.NullString
	if err := row.Scan(&t.ID, &t.Type, &t.Payload, &t.Priority, &t.Attempts, &t.MaxAttempts, &t.State, &t.NextRunAt, &t.VisibilityTimeout, &idem, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return domain.Task{}, err
	}
	if idem.Valid {
		s := idem.String
		t.IdempotencyKey = &s
	}
	return t, nil
}

func (r *sqliteRepo) ListRecentTasks(ctx context.Context, limit int) ([]domain.Task, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id,type,payload,priority,attempts,max_attempts,state,next_run_at,visibility_timeout,idempotency_key,created_at,updated_at
FROM tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		var t domain.Task
		var idem sql.NullString
		if err := rows.Scan(&t.ID, &t.Type, &t.Payload, &t.Priority, &t.Attempts, &t.MaxAttempts, &t.State, &t.NextRunAt, &t.VisibilityTimeout, &idem, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		if idem.Valid {
			s := idem.String
			t.IdempotencyKey = &s
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (r *sqliteRepo) CreateSchedule(ctx context.Context, s domain.Schedule) (string, error) {
	id := s.ID
	if id == "" {
		id = "sch_" + uuid.NewString()
	}
	if s.Priority == 0 {
		s.Priority = 5
	}
	if s.MaxAttempts == 0 {
		s.MaxAttempts = 5
	}

	_, err := r.db.ExecContext(ctx, `
INSERT INTO schedules (id,name,cron_expr,task_type,payload,priority,max_attempts,enabled,last_run,next_run,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)
`, id, s.Name, s.CronExpr, s.TaskType, s.Payload, s.Priority, s.MaxAttempts, s.Enabled, s.LastRun, s.NextRun)
	return id, err
}

func (r *sqliteRepo) GetSchedule(ctx context.Context, id string) (domain.Schedule, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id,name,cron_expr,task_type,payload,priority,max_attempts,enabled,last_run,next_run,created_at,updated_at
FROM schedules WHERE id=?`, id)
	var s domain.Schedule
	var lastRun sql.NullTime
	if err := row.Scan(&s.ID, &s.Name, &s.CronExpr, &s.TaskType, &s.Payload, &s.Priority, &s.MaxAttempts, &s.Enabled, &lastRun, &s.NextRun, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return domain.Schedule{}, err
	}
	if lastRun.Valid {
		s.LastRun = &lastRun.Time
	}
	return s, nil
}

func (r *sqliteRepo) ListSchedules(ctx context.Context) ([]domain.Schedule, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id,name,cron_expr,task_type,payload,priority,max_attempts,enabled,last_run,next_run,created_at,updated_at
FROM schedules ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []domain.Schedule
	for rows.Next() {
		var s domain.Schedule
		var lastRun sql.NullTime
		if err := rows.Scan(&s.ID, &s.Name, &s.CronExpr, &s.TaskType, &s.Payload, &s.Priority, &s.MaxAttempts, &s.Enabled, &lastRun, &s.NextRun, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			s.LastRun = &lastRun.Time
		}
		schedules = append(schedules, s)
	}
	return schedules, rows.Err()
}

func (r *sqliteRepo) UpdateSchedule(ctx context.Context, s domain.Schedule) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE schedules SET name=?,cron_expr=?,task_type=?,payload=?,priority=?,max_attempts=?,enabled=?,next_run=?,updated_at=CURRENT_TIMESTAMP
WHERE id=?`, s.Name, s.CronExpr, s.TaskType, s.Payload, s.Priority, s.MaxAttempts, s.Enabled, s.NextRun, s.ID)
	return err
}

func (r *sqliteRepo) DeleteSchedule(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM schedules WHERE id=?", id)
	return err
}

func (r *sqliteRepo) GetDueSchedules(ctx context.Context, now time.Time) ([]domain.Schedule, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT id,name,cron_expr,task_type,payload,priority,max_attempts,enabled,last_run,next_run,created_at,updated_at
FROM schedules WHERE enabled=1 AND next_run <= ? ORDER BY next_run`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var schedules []domain.Schedule
	for rows.Next() {
		var s domain.Schedule
		var lastRun sql.NullTime
		if err := rows.Scan(&s.ID, &s.Name, &s.CronExpr, &s.TaskType, &s.Payload, &s.Priority, &s.MaxAttempts, &s.Enabled, &lastRun, &s.NextRun, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if lastRun.Valid {
			s.LastRun = &lastRun.Time
		}
		schedules = append(schedules, s)
	}
	return schedules, rows.Err()
}

func (r *sqliteRepo) UpdateScheduleLastRun(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE schedules SET last_run=?,next_run=?,updated_at=CURRENT_TIMESTAMP WHERE id=?`, lastRun, nextRun, id)
	return err
}
