package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"

	"localflow/internal/api"
	httphandler "localflow/internal/handlers/http"
	"localflow/internal/handlers/shell"
	"localflow/internal/queue"
	"localflow/internal/scheduler"
	"localflow/internal/worker"
)

func main() {
	var (
		addr     = flag.String("addr", ":8080", "HTTP bind address")
		dbPath   = flag.String("db", "localflow.db", "SQLite DB path")
		workers  = flag.Int("workers", 8, "number of worker goroutines")
		poll     = flag.Duration("poll", 250*time.Millisecond, "poll interval for queue")
		debug    = flag.Bool("debug", false, "enable debug mode with pprof endpoints")
		schedInt = flag.Duration("schedule-interval", 10*time.Second, "schedule check interval")
	)
	flag.Parse()

	zerolog.TimeFieldFormat = time.RFC3339
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	dsn := fmt.Sprintf("file:%s?cache=shared&mode=rwc&_pragma=journal_mode(WAL)", *dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("open db")
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // SQLite single writer

	if err := queue.EnsureSchema(db); err != nil {
		log.Fatal().Err(err).Msg("ensure schema")
	}

	repo := queue.NewSQLiteRepo(db)
	if n, err := repo.RecoverStale(context.Background(), time.Now()); err == nil {
		log.Info().Int("recovered", n).Msg("recovered stale running tasks")
	}

	// Handlers registry
	handlers := map[string]worker.Handler{
		"shell": shell.Shell{},
		"http":  httphandler.HTTP{},
	}

	// Start worker pool
	ctx, cancel := context.WithCancel(context.Background())
	pool := worker.NewPool(repo, handlers, *workers, *poll)
	go pool.Run(ctx)

	// Start scheduler service
	schedulerSvc := scheduler.NewService(repo, *schedInt)
	go schedulerSvc.Start(ctx)

	// HTTP server with optional debug endpoints
	server := api.NewServerWithDebug(repo, *debug)
	if *debug {
		log.Info().Msg("debug mode enabled - pprof available at /debug/pprof/")
	}

	srv := &http.Server{Addr: *addr, Handler: server}
	go func() {
		log.Info().Str("addr", *addr).Bool("debug", *debug).Msg("HTTP server starting")
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
