# LocalFlow

A fully local, single-binary task processing platform written in Go.

## Features

* **SQLite‑backed durable queue** (CGO‑free via `modernc.org/sqlite`)
* **Worker pool** with retries/backoff + stale task recovery
* **REST API** (submit + status) using Chi
* **Cron-based scheduler** with periodic task enqueuing
* **HTTP task handler** for calling local/remote endpoints  
* **Idempotency enforcement** (duplicate prevention with keys)
* **Web dashboard** with HTMX for interactive management
* **Performance profiling** with pprof endpoints (debug mode)
* **Health and metrics endpoints**

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

* **shell**: executes local commands with timeout
* **http**: makes HTTP requests to local/remote endpoints

## API Endpoints

### Tasks
* `POST /api/tasks` - Submit a new task
* `GET /api/tasks/{id}` - Get task status

### Schedules
* `POST /api/schedules` - Create a new schedule
* `GET /api/schedules` - List all schedules  
* `GET /api/schedules/{id}` - Get schedule details
* `PUT /api/schedules/{id}` - Update a schedule
* `DELETE /api/schedules/{id}` - Delete a schedule

### System
* `GET /health` - Health check
* `GET /metrics` - Prometheus-style metrics

### Web Dashboard
* `GET /` or `/dashboard` - Interactive web interface
* `GET /dashboard/tasks` - Task list (HTMX fragment)
* `GET /dashboard/schedules` - Schedule list (HTMX fragment)

### Debug (when --debug enabled)
* `GET /debug/pprof/` - Performance profiling interface

## Configuration

Command line flags:
* `-addr`: HTTP bind address (default: `:8080`)
* `-db`: SQLite DB path (default: `localflow.db`)
* `-workers`: Number of worker goroutines (default: `8`)
* `-poll`: Poll interval for queue (default: `250ms`)
* `-schedule-interval`: Schedule check interval (default: `10s`)
* `-debug`: Enable debug mode with pprof endpoints

## Notes

* SQLite runs in WAL mode for better concurrency
* On startup, the system recovers stale `running` tasks whose visibility window expired
* Tasks support priority ordering, retry logic, and idempotency keys
* Exponential backoff for failed tasks with configurable max attempts

## Examples

### Submit a Shell Task
```bash
curl -X POST http://localhost:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"type":"shell","payload":{"command":"echo","args":["Hello, World!"]}}'
```

### Submit an HTTP Task  
```bash
curl -X POST http://localhost:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"type":"http","payload":{"url":"https://api.github.com","method":"GET","timeout":10}}'
```

### Create a Schedule (Every 5 Minutes)
```bash
curl -X POST http://localhost:8080/api/schedules \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "Health Check", 
    "cron_expr": "*/5 * * * *",
    "task_type": "http",
    "payload": {"url": "http://localhost:8080/health", "method": "GET"},
    "enabled": true
  }'
```

### Test Idempotency
```bash
# Submit same task twice with idempotency key - should return same ID
curl -X POST http://localhost:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"type":"shell","payload":{"command":"echo","args":["test"]},"idempotency_key":"unique-123"}'
```

## Web Dashboard

Visit `http://localhost:8080` in your browser for an interactive dashboard to:
* View real-time task status
* Submit new tasks with form validation  
* Create and manage schedules
* Monitor system performance

## Performance Monitoring

Run with `--debug` flag to enable pprof endpoints:
```bash
./localflow --debug
# Then visit http://localhost:8080/debug/pprof/
```

## Advanced Usage

### Custom Cron Expressions
LocalFlow uses standard 5-field cron expressions:
* `*/5 * * * *` - Every 5 minutes
* `0 9 * * MON-FRI` - 9 AM on weekdays  
* `0 0 1 * *` - First day of every month
* `30 14 * * 6` - 2:30 PM every Saturday
