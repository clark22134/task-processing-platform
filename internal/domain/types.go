package domain

import "time"

type Task struct {
	ID                string
	Type              string
	Payload           []byte
	Priority          int
	Attempts          int
	MaxAttempts       int
	State             string
	NextRunAt         time.Time
	VisibilityTimeout int // seconds
	IdempotencyKey    *string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Schedule struct {
	ID          string
	Name        string
	CronExpr    string
	TaskType    string
	Payload     []byte
	Priority    int
	MaxAttempts int
	Enabled     bool
	LastRun     *time.Time
	NextRun     time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
