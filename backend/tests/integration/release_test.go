package integration

import (
	"agent-platform/backend/internal/bootstrap"
	"agent-platform/backend/internal/config"
	"agent-platform/backend/internal/model"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestReleaseReadyWithoutModelAndRefusesSecondInstance(t *testing.T) {
	f := setup(t)
	dsn, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal("invalid test DSN")
	}
	q := dsn.Query()
	q.Set("search_path", f.pool.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = q.Encode()
	cfg, e := config.Load(func(k string) string {
		if k == "DB_DSN" {
			return dsn.String()
		}
		return ""
	})
	if e != nil {
		t.Fatal("configuration failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	app, e := bootstrap.New(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer app.Close()
	if w := request(app.Handler, "GET", "/health/ready", "", nil, ""); w.Code != 200 {
		t.Fatalf("not ready: %d", w.Code)
	}
	if second, e := bootstrap.New(ctx, cfg); e == nil {
		second.Close()
		t.Fatal("second instance accepted")
	}
	w := request(app.Handler, "POST", "/api/v1/auth/register", `{"username":"web-user","password":"password-123"}`, nil, "")
	if w.Code != 201 {
		t.Fatalf("register failed: %d", w.Code)
	}
	cookie, csrf := webLogin(t, app.Handler)
	w = request(app.Handler, "POST", "/api/v1/conversations", `{"agent_type":"general"}`, cookie, csrf)
	var conv model.Conversation
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &conv) != nil {
		t.Fatal("conversation create failed")
	}
	w = request(app.Handler, "POST", "/api/v1/conversations/"+conv.ID+"/runs", `{}`, cookie, csrf)
	var failure struct {
		Code string `json:"code"`
	}
	json.Unmarshal(w.Body.Bytes(), &failure)
	if w.Code != 503 || failure.Code != "model_unavailable" {
		t.Fatalf("unconfigured model was not rejected: %d %s", w.Code, failure.Code)
	}
}
