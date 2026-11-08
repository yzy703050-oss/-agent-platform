package runtimev1

import "time"

type RunView struct {
	ID, Status string
	DeadlineAt time.Time
	Version    int64
}
type JobLease struct {
	Ref                            ExecRef
	ScheduleSeq, CheckpointVersion int64
	DeadlineAt                     time.Time
	RunVersion                     int64
	Limits                         LimitSnapshot
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
	CostUpperMicros                                            int64
	ToolTimeout                                                time.Duration
	DeadlineAt                                                 time.Time
}
