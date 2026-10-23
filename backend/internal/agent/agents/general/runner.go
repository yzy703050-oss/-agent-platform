// Package general adapts Eino execution; it does not implement a second agent loop.
package general

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type RunnerConfig struct {
	Agent        adk.Agent
	Store        adk.CheckPointStore
	RunID        string
	Fingerprint  string
	Resume       bool
	PauseTimeout time.Duration
	Streaming    bool
	OnDelta      func(string) error
}
type RunResult struct {
	Resumable bool
	FinalText string
}
type Runner struct {
	cfg                               RunnerConfig
	store                             *checkpointStore
	mu                                sync.Mutex
	loop                              *adk.TurnLoop[string, *schema.Message]
	started, finished, pauseRequested bool
	result                            RunResult
}

func NewRunner(ctx context.Context, cfg RunnerConfig) (*Runner, error) {
	if cfg.Agent == nil || cfg.Store == nil || cfg.RunID == "" || cfg.Fingerprint == "" {
		return nil, errors.New("invalid runner configuration")
	}
	if cfg.PauseTimeout <= 0 {
		cfg.PauseTimeout = 15 * time.Second
	}
	s := &checkpointStore{inner: cfg.Store, runID: cfg.RunID, fingerprint: cfg.Fingerprint, requirePresent: cfg.Resume}
	if cfg.Resume {
		_, ok, e := s.Get(ctx, cfg.RunID)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, ErrCheckpointMissing
		}
	}
	return &Runner{cfg: cfg, store: s}, nil
}
func (r *Runner) Result() RunResult { r.mu.Lock(); defer r.mu.Unlock(); return r.result }
func (r *Runner) RequestPause() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loop == nil || r.finished {
		return false
	}
	r.pauseRequested = true
	r.loop.Stop(adk.WithGracefulTimeout(r.cfg.PauseTimeout))
	return true
}

// Run is single-use. Resume reads the original checkpoint and never fabricates a new user question.
func (r *Runner) Run(ctx context.Context, messages []*schema.Message, emit func(*schema.Message) error) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return errors.New("runner already started")
	}
	r.started = true
	cfg := adk.TurnLoopConfig[string, *schema.Message]{
		Store: r.store, CheckpointID: r.cfg.RunID,
		GenInput: func(_ context.Context, _ *adk.TurnLoop[string, *schema.Message], items []string) (*adk.GenInputResult[string, *schema.Message], error) {
			return &adk.GenInputResult[string, *schema.Message]{Input: &adk.AgentInput{Messages: messages, EnableStreaming: r.cfg.Streaming}, Consumed: items}, nil
		},
		GenResume: func(_ context.Context, _ *adk.TurnLoop[string, *schema.Message], interrupted, unhandled, newItems []string) (*adk.GenResumeResult[string, *schema.Message], error) {
			return &adk.GenResumeResult[string, *schema.Message]{Consumed: interrupted, Remaining: append(unhandled, newItems...)}, nil
		},
		PrepareAgent: func(context.Context, *adk.TurnLoop[string, *schema.Message], []string) (adk.Agent, error) {
			return r.cfg.Agent, nil
		},
		OnAgentEvents: func(_ context.Context, tc *adk.TurnContext[string, *schema.Message], events *adk.AsyncIterator[*adk.AgentEvent]) error {
			for {
				event, ok := events.Next()
				if !ok {
					break
				}
				if event.Err != nil {
					var ce *adk.CancelError
					if !errors.As(event.Err, &ce) {
						return event.Err
					}
					continue
				}
				if event.Output == nil || event.Output.MessageOutput == nil {
					continue
				}
				msg, e := r.readMessage(event.Output.MessageOutput)
				if e != nil {
					return e
				}
				if msg == nil {
					continue
				}
				if msg.Role == schema.Assistant && len(msg.ToolCalls) == 0 {
					r.mu.Lock()
					r.result.FinalText = msg.Content
					r.mu.Unlock()
				}
				if emit != nil {
					if e = emit(msg); e != nil {
						return e
					}
				}
			}
			r.mu.Lock()
			paused := r.pauseRequested
			r.mu.Unlock()
			if !paused {
				tc.Loop.Stop(adk.WithSkipCheckpoint())
			}
			return nil
		},
	}
	r.loop = adk.NewTurnLoop(cfg)
	loop := r.loop
	if !r.cfg.Resume {
		loop.Push("start")
	}
	loop.Run(ctx)
	r.mu.Unlock()
	exit := loop.Wait()
	r.mu.Lock()
	r.finished = true
	paused := r.pauseRequested
	r.mu.Unlock()
	if exit.CheckpointErr != nil {
		return fmt.Errorf("checkpoint save failed: %w", exit.CheckpointErr)
	}
	if paused {
		if !exit.CheckpointAttempted {
			return errors.New("pause ended without checkpoint")
		}
		_, ok, e := r.store.Get(ctx, r.cfg.RunID)
		if e != nil {
			return e
		}
		if !ok {
			return ErrCheckpointMissing
		}
		r.mu.Lock()
		r.result.Resumable = true
		r.mu.Unlock()
		return nil
	}
	return exit.ExitReason
}

func (r *Runner) readMessage(out *adk.MessageVariant) (*schema.Message, error) {
	if !out.IsStreaming {
		return out.GetMessage()
	}
	defer out.MessageStream.Close()
	chunks := []*schema.Message{}
	size := 0
	for {
		chunk, e := out.MessageStream.Recv()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if chunk == nil {
			continue
		}
		size += len(chunk.Content)
		for _, call := range chunk.ToolCalls {
			size += len(call.Function.Arguments)
		}
		if size > 256000 {
			return nil, errors.New("model output limit exceeded")
		}
		chunks = append(chunks, chunk)
		if (out.Role == schema.Assistant || chunk.Role == schema.Assistant) && chunk.Content != "" && r.cfg.OnDelta != nil {
			runes := []rune(chunk.Content)
			for len(runes) > 0 {
				n := len(runes)
				if n > 1000 {
					n = 1000
				}
				if e = r.cfg.OnDelta(string(runes[:n])); e != nil {
					return nil, e
				}
				runes = runes[n:]
			}
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}
	return schema.ConcatMessages(chunks)
}
