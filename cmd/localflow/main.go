package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"

	"localflow/internal/api"
	"localflow/internal/handlers/shell"
	"localflow/internal/queue"
	"localflow/internal/worker"
)

func main() {
	var (
		addr   = flag.String("addr", ":8080", "HTTP bind address")
		dbPath = flag.String("db", "localflow.db", "SQLite DB path")
		workers = flag.Int("workers", 8, "number of worker goroutines")
		poll    = flag.Duration("poll", 250*time.Millisecond, "poll interval for queue")
	)
	flag.Parse()

	zerolog.TimeFieldFormat = time.RFC3339
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc&_pragma=journal_mode(WAL)", *dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil { log.Fatal().Err(err).Msg("open db") }
	defer db.Close()
	db.SetMaxOpenConns(1) // SQLite single writer

	if err := queue.EnsureSchema(db); err != nil { log.Fatal().Err(err).Msg("ensure schema") }

	repo := queue.NewSQLiteRepo(db)
	if n, err := repo.RecoverStale(context.Background(), time.Now()); err == nil {
		log.Info().Int("recovered", n).Msg("recovered stale running tasks")
	}

	// Handlers registry
	handlers := map[string]worker.Handler{
		"shell": shell.Shell{},
	}

	// Start worker pool
	ctx, cancel := context.WithCancel(context.Background())
	pool := worker.NewPool(repo, handlers, *workers, *poll)
	go pool.Run(ctx)

	// HTTP server
	srv := &http.Server{Addr: *addr, Handler: api.NewServer(repo)}
	go func() {
		log.Info().Str("addr", *addr).Msg("HTTP server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("http server")
		}
	}()

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	log.Info().Msg("shutting down")
	cancel()
	ctxTimeout, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTimeout()
	_ = srv.Shutdown(ctxTimeout)
}