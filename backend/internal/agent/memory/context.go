// Package memory assembles authorized history without coupling the platform to Eino.
package memory

import (
	"agent-platform/backend/internal/agent/ports"
	"agent-platform/backend/internal/model"
	"context"
)

type ContextBuilder struct {
	store    ports.MessageStore
	maxBytes int
}

func New(s ports.MessageStore, maxBytes int) *ContextBuilder { return &ContextBuilder{s, maxBytes} }

// Retain whole runs so tool-call/result groups are never cut in half.
func (b *ContextBuilder) Build(ctx context.Context, s model.Scope, conv, run string) ([]model.Message, error) {
	all, e := b.store.ListContext(ctx, s, conv, run)
	if e != nil {
		return nil, e
	}
	return retainRuns(all, b.maxBytes)
}

func retainRuns(all []model.Message, maxBytes int) ([]model.Message, error) {
	start, total := len(all), 0
	for end := len(all); end > 0; {
		begin := end - 1
		id := all[begin].RunID
		for begin > 0 && all[begin-1].RunID == id {
			begin--
		}
		size := 0
		for _, m := range all[begin:end] {
			size += messageBytes(m)
		}
		if total+size > maxBytes {
			if end == len(all) {
				return nil, model.ErrInvalidInput
			}
			break
		}
		total += size
		start = begin
		end = begin
	}
	return all[start:], nil
}

func messageBytes(m model.Message) int {
	size := len(m.Content) + len(m.ToolCallID) + 32
	for _, call := range m.ToolCalls {
		size += len(call.Arguments) + len(call.Name) + len(call.ID)
	}
	return size
}
