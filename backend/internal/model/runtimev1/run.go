package runtimev1

import "time"

type RunView struct {
	ID, Status string
	DeadlineAt time.Time
}
type JobLease struct {
	Ref                            ExecRef
	ScheduleSeq, CheckpointVersion int64
	DeadlineAt                     time.Time
}
type TravelTaskLease struct {
	TaskID            string
	Ref               ExecRef
	CheckpointVersion int64
	DeadlineAt        time.Time
}
type LimitSnapshot struct {
	MaxModelAttempts, MaxToolAttempts                          int
	TokenUpperBound, MaxModelInputTokens, MaxModelOutputTokens int64
	ToolTimeout                                                time.Duration
	DeadlineAt                                                 time.Time
}
