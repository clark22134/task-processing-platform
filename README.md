# LocalFlow

A fully local, single-binary task processing platform written in Go.

## Features

* **SQLite‑backed durable queue** (CGO‑free via `modernc.org/sqlite`)
* **Worker pool** with retries/backoff + stale task recovery
* **Minimal REST API** (submit + status) using Chi
* **Simple metrics and health endpoints**

## Quick Start

```bash
# From repo root
make tidy
make run
# Server at http://127.0.0.1:8080
```

### Submit a task

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks \
  -H 'content-type: application/json' \
  -d '{"type":"shell","payload":{"command":"echo","args":["Hello, LocalFlow!"]}}'
```

### Check status

```bash
curl -s http://127.0.0.1:8080/api/tasks/<TASK_ID>
```

## Project Structure

```
localflow/
├─ cmd/
│  └─ localflow/
│     └─ main.go
├─ internal/
│  ├─ api/
│  │  └─ server.go
│  ├─ queue/
│  │  └─ sqlite.go
│  ├─ worker/
│  │  └─ pool.go
│  ├─ handlers/
│  │  └─ shell/
│  │     └─ shell.go
│  └─ domain/
│     └─ types.go
├─ go.mod
├─ Makefile
├─ README.md
└─ migrations/
   └─ 001_init.sql
```

## Handlers

* **shell**: executes a local command with context timeout (from task visibility).

## API Endpoints

* `POST /api/tasks` - Submit a new task
* `GET /api/tasks/{id}` - Get task status
* `GET /health` - Health check
* `GET /metrics` - Prometheus-style metrics

## Configuration

Command line flags:
* `-addr`: HTTP bind address (default: `:8080`)
* `-db`: SQLite DB path (default: `localflow.db`)
* `-workers`: Number of worker goroutines (default: `8`)
* `-poll`: Poll interval for queue (default: `250ms`)

## Notes

* SQLite runs in WAL mode for better concurrency
* On startup, the system recovers stale `running` tasks whose visibility window expired
* Tasks support priority ordering, retry logic, and idempotency keys
* Exponential backoff for failed tasks with configurable max attempts

## Smoke Test

```bash
# 1) Run the server
make run

# 2) New shell task
curl -s -X POST :8080/api/tasks \
 -H 'content-type: application/json' \
 -d '{"type":"shell","payload":{"command":"bash","args":["-lc","echo $(date) && sleep 1 && echo done"]}}'

# 3) Replace <ID> with returned id and poll status
curl -s :8080/api/tasks/<ID>
```

## Next Steps (Future Upgrades)

* Add `/api/schedules` with cron parsing and periodic enqueues
* Add `http` handler for calling local endpoints
* Add idempotency enforcement on `Enqueue` (return existing id if key collides)
* Add a minimal web dashboard (htmx + Go templates) over the same server
* Add pprof behind `--debug`
