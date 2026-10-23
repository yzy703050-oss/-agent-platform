package agenttools

import (
	"agent-platform/backend/internal/model"
	"context"
	"testing"
	"time"
)

type settingsReader struct {
	seen  model.Scope
	calls int
}

func (r *settingsReader) Get(_ context.Context, s model.Scope) (model.UserSettings, error) {
	r.seen = s
	r.calls++
	return model.UserSettings{Theme: "dark"}, nil
}
func TestGetMySettingsIgnoresForgedUserID(t *testing.T) {
	r := &settingsReader{}
	tool := NewSettingsTool(r, model.Scope{UserID: "trusted"}, time.Second)
	if _, e := tool.InvokableRun(context.Background(), `{"user_id":"victim"}`); e == nil {
		t.Fatal("forged field accepted")
	}
	if r.calls != 0 {
		t.Fatal("unexpected access")
	}
	out, e := tool.InvokableRun(context.Background(), `{}`)
	if e != nil || r.seen.UserID != "trusted" || out == "" {
		t.Fatalf("scoped tool: %s %v", out, e)
	}
}
