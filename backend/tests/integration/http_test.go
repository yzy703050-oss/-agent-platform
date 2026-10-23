package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agent-platform/backend/internal/router"
	authsvc "agent-platform/backend/internal/service/auth"
	convsvc "agent-platform/backend/internal/service/conversation"
	settingssvc "agent-platform/backend/internal/service/settings"
	usersvc "agent-platform/backend/internal/service/user"
)

func testRouter(t *testing.T) (http.Handler, *fixture) {
	t.Helper()
	f := setup(t)
	a := authsvc.New(f.repo)
	if _, e := a.Register(context.Background(), "web-user", "password-123"); e != nil {
		t.Fatal(e)
	}
	return router.New(router.Dependencies{Auth: a, Users: usersvc.New(f.repo), Conversations: convsvc.New(f.repo), Settings: settingssvc.New(f.repo), AllowedOrigin: "http://127.0.0.1:5173"}), f
}
func TestAuthRateLimit(t *testing.T) {
	h, _ := testRouter(t)
	for i := 0; i < 11; i++ {
		w := request(h, "POST", "/api/v1/auth/login", `{"username":"missing","password":"incorrect"}`, nil, "")
		if i < 10 && w.Code != 401 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
		if i == 10 && w.Code != 429 {
			t.Fatalf("expected rate limit, got %d", w.Code)
		}
	}
}

func request(h http.Handler, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func webLogin(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	w := request(h, "POST", "/api/v1/auth/login", `{"username":"web-user","password":"password-123"}`, nil, "")
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe session cookie")
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	return cookies[0], body.CSRF
}
func TestCSRFMissing(t *testing.T) {
	h, _ := testRouter(t)
	cookie, csrf := webLogin(t, h)
	w := request(h, "PATCH", "/api/v1/settings", `{"theme":"dark","reply_preference":""}`, cookie, "")
	if w.Code != 403 {
		t.Fatalf("csrf missing: %d", w.Code)
	}
	w = request(h, "PATCH", "/api/v1/settings", `{"theme":"dark","reply_preference":""}`, cookie, csrf)
	if w.Code != 200 {
		t.Fatalf("valid csrf: %d %s", w.Code, w.Body.String())
	}
	w = request(h, "POST", "/api/v1/auth/logout", "{}", cookie, csrf)
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w = request(h, "GET", "/api/v1/me", "", cookie, ""); w.Code != 401 {
		t.Fatalf("revoked cookie: %d", w.Code)
	}
}

func TestCurrentUserNoPasswordHash(t *testing.T) {
	h, _ := testRouter(t)
	cookie, _ := webLogin(t, h)
	w := request(h, "GET", "/api/v1/me", "", cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "web-user") || strings.Contains(w.Body.String(), "password_hash") {
		t.Fatalf("profile: %d %s", w.Code, w.Body.String())
	}
}
func TestCrossOriginWriteRejected(t *testing.T) {
	h, _ := testRouter(t)
	cookie, csrf := webLogin(t, h)
	r := httptest.NewRequest("PATCH", "/api/v1/settings", strings.NewReader(`{"theme":"dark"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.test")
	r.Header.Set("X-CSRF-Token", csrf)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestTravelUnavailable(t *testing.T) {
	h, f := testRouter(t)
	cookie, csrf := webLogin(t, h)
	w := request(h, "POST", "/api/v1/conversations", `{"agent_type":"travel","title":"trip"}`, cookie, csrf)
	if w.Code != 201 {
		t.Fatalf("create: %s", w.Body.String())
	}
	var conv struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &conv)
	w = request(h, "POST", "/api/v1/conversations/"+conv.ID+"/runs", `{"content":"plan a trip"}`, cookie, csrf)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "agent_unavailable") {
		t.Fatalf("travel: %d %s", w.Code, w.Body.String())
	}
	var count int
	f.pool.QueryRow(context.Background(), "SELECT count(*) FROM agent_runs").Scan(&count)
	if count != 0 {
		t.Fatal("travel started a fake run")
	}
}
