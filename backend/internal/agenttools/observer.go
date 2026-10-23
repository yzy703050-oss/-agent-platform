package agenttools

import (
	"agent-platform/backend/internal/agent/contracts"
	"context"
)

type observerKey struct{}

func WithObserver(ctx context.Context, emit func(contracts.Event) error) context.Context {
	return context.WithValue(ctx, observerKey{}, emit)
}
func Emit(ctx context.Context, event contracts.Event) error {
	emit, _ := ctx.Value(observerKey{}).(func(contracts.Event) error)
	if emit == nil {
		return nil
	}
	return emit(event)
}
