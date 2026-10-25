package api

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gammichdb "github.com/klNuno/gammich/internal/db"
)

func TestRequireMutateAuth(t *testing.T) {
	s := &Server{
		IngestToken: "itok",
		ProxySecret: "psecret",
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := s.requireMutateAuth(next)

	cases := []struct {
		name       string
		remoteAddr string // "" keeps httptest default (non-loopback 192.0.2.1)
		headers    map[string]string
		want       int
	}{
		{"no creds", "", nil, http.StatusForbidden},
		{"proxy secret", "", map[string]string{"X-Gammich-Proxy-Secret": "psecret"}, http.StatusOK},
		// A reverse proxy on the same host makes every visitor a loopback
		// peer, so loopback is never trusted unless TrustLoopback is set.
		{"loopback peer", "127.0.0.1:5555", nil, http.StatusForbidden},
		{"forged remote-user only", "", map[string]string{"Remote-User": "admin", "Remote-Groups": "admins"}, http.StatusForbidden},
		{"ingest token", "", map[string]string{"X-Gammich-Token": "itok"}, http.StatusOK},
		{"forged session cookie", "", map[string]string{"Cookie": "gammich_session=v1.9999999999.AAAA"}, http.StatusForbidden},
		{"wrong proxy secret", "", map[string]string{"X-Gammich-Proxy-Secret": "nope"}, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/clips/bulk-tag", nil)
			if c.remoteAddr != "" {
				req.RemoteAddr = c.remoteAddr
			}
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

func TestTrustLoopbackOptIn(t *testing.T) {
	s := &Server{TrustLoopback: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.requireMutateAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	for addr, want := range map[string]int{"127.0.0.1:5555": http.StatusOK, "[::1]:5555": http.StatusOK, "192.0.2.1:5555": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status = %d, want %d", addr, rec.Code, want)
		}
	}
}

func newPasswordServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s := &Server{
		DB:        openTestDB(t),
		VideoRoot: t.TempDir(),
		Password:  "hunter2",
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return s, s.Router()
}

func doReq(h http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPasswordModeGatesReadsAndEdits(t *testing.T) {
	_, h := newPasswordServer(t)

	if rec := doReq(h, http.MethodGet, "/api/clips", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: status = %d, want 401", rec.Code)
	}
	if rec := doReq(h, http.MethodPost, "/api/tags", `{"name":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous edit: status = %d, want 401", rec.Code)
	}
	if rec := doReq(h, http.MethodGet, "/api/healthz?lite=1", ""); rec.Code != http.StatusOK {
		t.Fatalf("healthz: status = %d, want 200", rec.Code)
	}
	if rec := doReq(h, http.MethodPost, "/api/login", `{"password":"nope"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401", rec.Code)
	}

	rec := doReq(h, http.MethodPost, "/api/login", `{"password":"hunter2"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("login: status = %d, want 204", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("login cookie = %+v", cookies)
	}

	if rec := doReq(h, http.MethodGet, "/api/clips", "", cookies...); rec.Code != http.StatusOK {
		t.Fatalf("logged-in read: status = %d, want 200", rec.Code)
	}
	if rec := doReq(h, http.MethodPost, "/api/tags", `{"name":"x"}`, cookies...); rec.Code >= 400 {
		t.Fatalf("logged-in edit: status = %d, want success", rec.Code)
	}

	rec = doReq(h, http.MethodGet, "/api/session", "", cookies...)
	if !strings.Contains(rec.Body.String(), `"auth":"password"`) ||
		!strings.Contains(rec.Body.String(), `"can_edit":true`) ||
		!strings.Contains(rec.Body.String(), `"videos_ok":true`) {
		t.Fatalf("session body = %s", rec.Body.String())
	}
}

func TestPasswordChangeInvalidatesSessions(t *testing.T) {
	s, _ := newPasswordServer(t)
	token := s.signSession(time.Now().Add(time.Hour))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	if !s.validSession(req) {
		t.Fatal("fresh session rejected")
	}
	s.Password = "changed"
	if s.validSession(req) {
		t.Fatal("session survived a password change")
	}
	s.Password = "hunter2"
	expired := httptest.NewRequest(http.MethodGet, "/", nil)
	expired.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.signSession(time.Now().Add(-time.Second))})
	if s.validSession(expired) {
		t.Fatal("expired session accepted")
	}
}

func TestLoginThrottle(t *testing.T) {
	_, h := newPasswordServer(t)
	for i := 0; i < loginMaxFailures; i++ {
		doReq(h, http.MethodPost, "/api/login", `{"password":"nope"}`)
	}
	if rec := doReq(h, http.MethodPost, "/api/login", `{"password":"hunter2"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: status = %d, want 429", loginMaxFailures, rec.Code)
	}
}

func TestCrossSiteEditRefused(t *testing.T) {
	s, h := newPasswordServer(t)
	c := &http.Cookie{Name: sessionCookie, Value: s.signSession(time.Now().Add(time.Hour))}
	req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(`{"name":"x"}`))
	req.AddCookie(c)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site edit: status = %d, want 403", rec.Code)
	}
}

func TestOpenModeIsReadOnly(t *testing.T) {
	s := &Server{DB: openTestDB(t), VideoRoot: t.TempDir(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.Router()
	if rec := doReq(h, http.MethodGet, "/api/clips", ""); rec.Code != http.StatusOK {
		t.Fatalf("open read: status = %d, want 200", rec.Code)
	}
	if rec := doReq(h, http.MethodPost, "/api/tags", `{"name":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("open edit: status = %d, want 403", rec.Code)
	}
	rec := doReq(h, http.MethodGet, "/api/session", "")
	if !strings.Contains(rec.Body.String(), `"auth":"none"`) || !strings.Contains(rec.Body.String(), `"can_edit":false`) {
		t.Fatalf("session body = %s", rec.Body.String())
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := gammichdb.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := gammichdb.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// Parallel attempts must not all pass a check made before any of them failed.
func TestLoginThrottleConcurrent(t *testing.T) {
	_, h := newPasswordServer(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doReq(h, http.MethodPost, "/api/login", `{"password":"nope"}`)
		}()
	}
	wg.Wait()
	if rec := doReq(h, http.MethodPost, "/api/login", `{"password":"hunter2"}`); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 20 parallel failures: status = %d, want 429", rec.Code)
	}
}

func TestLoginSuccessIsNotCounted(t *testing.T) {
	_, h := newPasswordServer(t)
	for i := 0; i < loginMaxFailures+2; i++ {
		if rec := doReq(h, http.MethodPost, "/api/login", `{"password":"hunter2"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("login %d: status = %d, want 204", i, rec.Code)
		}
	}
}

// Behind a trusted proxy, one client's failures must not lock out another.
func TestLoginThrottlePerForwardedClient(t *testing.T) {
	s, h := newPasswordServer(t)
	var err error
	if s.TrustedProxies, err = ParseCIDRs("10.0.0.1, 192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	login := func(client, password string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"`+password+`"}`))
		req.RemoteAddr = "10.0.0.1:443"
		req.Header.Set("X-Forwarded-For", "6.6.6.6, "+client)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < loginMaxFailures; i++ {
		login("203.0.113.9", "nope")
	}
	if code := login("203.0.113.9", "hunter2"); code != http.StatusTooManyRequests {
		t.Fatalf("attacker: status = %d, want 429", code)
	}
	if code := login("198.51.100.7", "hunter2"); code != http.StatusNoContent {
		t.Fatalf("other client: status = %d, want 204", code)
	}
}

func TestSessionEditNeedsSameOrigin(t *testing.T) {
	s, h := newPasswordServer(t)
	c := &http.Cookie{Name: sessionCookie, Value: s.signSession(time.Now().Add(time.Hour))}
	cases := []struct {
		name    string
		headers map[string]string
		ok      bool
	}{
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"sibling subdomain", map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.example.com"}, false},
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"origin matches host", map[string]string{"Origin": "http://example.com"}, true},
		{"origin differs", map[string]string{"Origin": "https://evil.example.com"}, false},
		{"no browser headers", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/tags", strings.NewReader(`{"name":"`+strings.ReplaceAll(tc.name, " ", "-")+`"}`))
			req.AddCookie(c)
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if got := rec.Code < 400; got != tc.ok {
				t.Fatalf("status = %d, want ok=%v", rec.Code, tc.ok)
			}
		})
	}
}
