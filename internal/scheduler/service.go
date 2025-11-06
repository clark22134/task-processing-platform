package scheduler

import (
	"context"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
	"localflow/internal/domain"
	"localflow/internal/queue"
)

type Service struct {
	repo     queue.Repository
	cron     *cron.Cron
	stop     chan struct{}
	interval time.Duration
}

func NewService(repo queue.Repository, checkInterval time.Duration) *Service {
	return &Service{
		repo:     repo,
		cron:     cron.New(),
		stop:     make(chan struct{}),
		interval: checkInterval,
	}
}

func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	log.Info().Dur("interval", s.interval).Msg("schedule service started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.processDueSchedules(ctx, now)
		}
	}
}

func (s *Service) Stop() {
	close(s.stop)
}

func (s *Service) processDueSchedules(ctx context.Context, now time.Time) {
	schedules, err := s.repo.GetDueSchedules(ctx, now)
	if err != nil {
		log.Error().Err(err).Msg("failed to get due schedules")
		return
	}

	for _, schedule := range schedules {
		if err := s.processSchedule(ctx, schedule, now); err != nil {
			log.Error().Err(err).Str("schedule_id", schedule.ID).Msg("failed to process schedule")
		}
	}
}

func (s *Service) processSchedule(ctx context.Context, schedule domain.Schedule, now time.Time) error {
	// Parse cron expression to get next run time
	cronSchedule, err := cron.ParseStandard(schedule.CronExpr)
	if err != nil {
		log.Error().Err(err).Str("cron_expr", schedule.CronExpr).Msg("invalid cron expression")
		return err
	}

	// Enqueue the task
	task := domain.Task{
		Type:        schedule.TaskType,
		Payload:     schedule.Payload,
		Priority:    schedule.Priority,
		MaxAttempts: schedule.MaxAttempts,
	}

	taskID, err := s.repo.Enqueue(ctx, task)
	if err != nil {
		log.Error().Err(err).Str("schedule_id", schedule.ID).Msg("failed to enqueue scheduled task")
		return err
	}

	// Calculate next run time
	nextRun := cronSchedule.Next(now)

	// Update schedule's last run and next run
	if err := s.repo.UpdateScheduleLastRun(ctx, schedule.ID, now, nextRun); err != nil {
		log.Error().Err(err).Str("schedule_id", schedule.ID).Msg("failed to update schedule run times")
		return err
	}

	log.Info().
		Str("schedule_id", schedule.ID).
		Str("schedule_name", schedule.Name).
		Str("task_id", taskID).
		Time("next_run", nextRun).
		Msg("scheduled task enqueued")

	return nil
}

// ValidateCronExpression validates a cron expression
func ValidateCronExpression(expr string) error {
	_, err := cron.ParseStandard(expr)
	return err
}

// NextRunTime calculates the next run time for a cron expression
func NextRunTime(expr string, from time.Time) (time.Time, error) {
	cronSchedule, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, err
	}
	return cronSchedule.Next(from), nil
}
