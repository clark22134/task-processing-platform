package worker

import (
	"context"
	"encoding/json"
	"time"

	"localflow/internal/domain"
	"localflow/internal/queue"
)

type Handler interface {
	Handle(ctx context.Context, payload json.RawMessage) error
}

type Pool struct {
	repo     queue.Repository
	handlers map[string]Handler
	sem      chan struct{}
	stop     chan struct{}
	pollEvery time.Duration
}

func NewPool(repo queue.Repository, handlers map[string]Handler, size int, pollEvery time.Duration) *Pool {
	return &Pool{repo: repo, handlers: handlers, sem: make(chan struct{}, size), stop: make(chan struct{}), pollEvery: pollEvery}
}

func (p *Pool) Run(ctx context.Context) {
	t := time.NewTicker(p.pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case now := <-t.C:
			for {
				task, lease, err := p.repo.LeaseNext(ctx, now)
				if err != nil { break }
				_ = lease // reserved for future
				p.sem <- struct{}{}
				go func(tk domain.Task) {
					defer func(){ <-p.sem }()
					h, ok := p.handlers[tk.Type]
					if !ok {
						_ = p.repo.Fail(ctx, tk.ID, "no handler", 0)
						return
					}
					c, cancel := context.WithTimeout(ctx, time.Duration(tk.VisibilityTimeout)*time.Second)
					defer cancel()
					if err := h.Handle(c, tk.Payload); err != nil {
						next := backoffExp(tk.Attempts)
						_ = p.repo.Retry(ctx, tk.ID, err.Error(), next)
						return
					}
					_ = p.repo.Succeed(ctx, tk.ID)
				}(task)
			}
		}
	}
}

func backoffExp(attempts int) time.Duration {
	if attempts <= 0 { return time.Second }
	d := 1 << (attempts-1) // 1,2,4,8...
	if d > 60 { d = 60 }
	return time.Duration(d) * time.Second
}