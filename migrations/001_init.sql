PRAGMA journal_mode=WAL;

CREATE TABLE tasks (
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

CREATE INDEX idx_tasks_next_run ON tasks(state, next_run_at, priority DESC);
CREATE UNIQUE INDEX idx_tasks_idem ON tasks(idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE task_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at DATETIME,
  success INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);