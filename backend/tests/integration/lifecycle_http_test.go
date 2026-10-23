package integration

import (
	chatapp "agent-platform/backend/internal/application/chat"
	"agent-platform/backend/internal/model"
	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPPageLeaseAndManualRecovery(t *testing.T) {
	f, m, l, a, _ := recoveryFixture(t)
	ctx := context.Background()
	auth := authsvc.New(f.repo)
	if _, e := auth.Register(ctx, "web-user", "password-123"); e != nil {
		t.Fatal(e)
	}
	convs := convsvc.New(f.repo)
	h := router.New(router.Dependencies{Auth: auth, Users: usersvc.New(f.repo), Conversations: convs, Settings: settingssvc.New(f.repo), Chat: chatapp.New(convs, m), Manager: m, Pages: l})
	cookie, csrf := webLogin(t, h)
	scope, e := auth.Authenticate(ctx, cookie.Value)
	if e != nil {
		t.Fatal(e)
	}
	f.scope = scope
	conv, e := convs.Create(ctx, scope, "general", "http")
	if e != nil {
		t.Fatal(e)
	}
	f.conversation = conv.ID
	pageResp := request(h, "POST", "/api/v1/pages", "{}", cookie, csrf)
	if pageResp.Code != 201 {
		t.Fatal(pageResp.Body.String())
	}
	var page model.PageLease
	json.Unmarshal(pageResp.Body.Bytes(), &page)
	call := func(path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		r.Header.Set("X-Page-Token", token)
		r.Header.Set("Idempotency-Key", "http-task")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	body := `{"page_id":"` + page.PageID + `","content":"question"}`
	path := "/api/v1/conversations/" + conv.ID + "/runs"
	if w := call(path, body, "forged"); w.Code != 404 {
		t.Fatalf("page id alone accepted: %d", w.Code)
	}
	w := call(path, body, page.Token)
	if w.Code != 202 {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	var run model.Run
	json.Unmarshal(w.Body.Bytes(), &run)
	<-a.inputs
	conflictRequest := httptest.NewRequest("POST", path, strings.NewReader(body))
	conflictRequest.AddCookie(cookie)
	conflictRequest.Header.Set("Content-Type", "application/json")
	conflictRequest.Header.Set("X-CSRF-Token", csrf)
	conflictRequest.Header.Set("X-Page-Token", page.Token)
	conflictRequest.Header.Set("Idempotency-Key", "different-request")
	conflictResponse := httptest.NewRecorder()
	h.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != 409 || !strings.Contains(conflictResponse.Body.String(), `"run_conflict"`) {
		t.Fatalf("run conflict code: %d %s", conflictResponse.Code, conflictResponse.Body.String())
	}
	path = "/api/v1/runs/" + run.ID
	if w = call(path+"/pause", "{}", ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	m.Wait()
	if w = request(h, "GET", path, "", cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"paused"`) {
		t.Fatalf("read paused: %d %s", w.Code, w.Body.String())
	}
	if a.starts.Load() != 1 {
		t.Fatal("GET auto-resumed")
	}
	if w = call(path+"/resume", `{"page_id":"`+page.PageID+`"}`, page.Token); w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	<-a.inputs
	if w = call(path+"/cancel", "{}", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"cancelled"`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
}
func TestRevokedSessionCannotReadSettingsToolData(t *testing.T) {
	f := setup(t)
	f.pool.Exec(context.Background(), "UPDATE sessions SET revoked_at=now() WHERE id=$1", f.scope.SessionID)
	if _, e := settingssvc.New(f.repo).Get(context.Background(), f.scope); e == nil {
		t.Fatal("revoked task can read settings")
	}
}
