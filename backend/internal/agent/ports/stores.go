package ports

import (
	"agent-platform/backend/internal/model"
	"context"
)

type RunStore interface {
	AcceptRun(context.Context, model.AdmitRunInput) (model.Run, error)
	SupersedePausedRun(context.Context, model.AdmitRunInput) (model.Run, error)
	CompleteRun(context.Context, model.FinishRunInput) error
	FinalizePause(context.Context, model.FinalizePauseInput) (model.Run, error)
}
type MessageStore interface {
	ListContext(context.Context, model.Scope, string, string) ([]model.Message, error)
}
type CheckpointStore interface {
	GetCheckpoint(context.Context, model.Scope, string) (model.Checkpoint, bool, error)
	SaveCheckpoint(context.Context, model.CheckpointWrite) error
	InvalidateCheckpoint(context.Context, model.Scope, string, int64) error
}
