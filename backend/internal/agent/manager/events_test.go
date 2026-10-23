package manager

import (
	"agent-platform/backend/internal/agent/contracts"
	"testing"
)

func TestSlowSubscriberBounded(t *testing.T) {
	h := NewEventHub(2)
	ch, stop, e := h.Subscribe("run")
	if e != nil {
		t.Fatal(e)
	}
	defer stop()
	for i := 0; i < 100; i++ {
		h.Publish(contracts.Event{RunID: "run", Type: "message.delta"})
	}
	n := 0
	for range ch {
		n++
		if n > 2 {
			t.Fatal("unbounded subscriber")
		}
	}
}
func TestSubscribePersistRaceNoDuplicate(t *testing.T) {
	c := NewEventCursor(0)
	got := []int64{}
	for _, seq := range []int64{1, 2, 2, 1, 3} {
		if c.Accept(contracts.Event{Seq: seq}) {
			got = append(got, seq)
		}
	}
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("duplicates: %v", got)
	}
	if !c.Accept(contracts.Event{Type: "message.delta"}) {
		t.Fatal("ephemeral delta incorrectly deduplicated")
	}
}
