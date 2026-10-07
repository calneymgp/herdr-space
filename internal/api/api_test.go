package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"herdr-space/internal/auth"
	"herdr-space/internal/model"
	"herdr-space/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeProvider struct{}

type spaceProvider struct{ fakeProvider }

func (spaceProvider) Inventory(context.Context) (model.Inventory, error) {
	return model.Inventory{Items: []model.Session{}, Spaces: []model.Space{{ID: "server:workspace:one", Name: "Workspace", ServerID: "server", WorkspaceID: "one"}}}, nil
}

type cancelOnWrite struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w cancelOnWrite) Write(p []byte) (int, error) {
	n, e := w.ResponseRecorder.Write(p)
	w.cancel()
	return n, e
}

func (fakeProvider) Inventory(context.Context) (model.Inventory, error) {
	return model.Inventory{Items: []model.Session{}}, nil
}
func (fakeProvider) Stop(context.Context, string, bool) error { return nil }
func (fakeProvider) Resume(context.Context, string, model.StartRequest) (model.Session, error) {
	return model.Session{}, nil
}
func (fakeProvider) Launch(context.Context, model.StartRequest) (model.Session, error) {
	return model.Session{}, nil
}
func (fakeProvider) Attach(context.Context, model.StreamRequest) (model.TerminalStream, error) {
	return nil, nil
}
func TestAuthCSRFAndAPIMiss(t *testing.T) {
	dir := t.TempDir()
	s, e := New(Config{DataDir: dir, Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	st, e := store.Open(filepath.Join(dir, "space.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	a, e := auth.New(st, filepath.Join(dir, "auth.key"))
	if e != nil {
		t.Fatal(e)
	}
	secret, _, e := a.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/api/v1/projects", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("unauth %d", rr.Code)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "correct horse battery staple", "otp": auth.Code(secret, time.Now())})
	req = httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Origin", "https://example.test")
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login %d %s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Cookies()[0]
	req = httptest.NewRequest("POST", "/api/v1/projects", bytes.NewBufferString(`{"name":"A","path":"/tmp"}`))
	req.Header.Set("Origin", "https://example.test")
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	req = httptest.NewRequest("GET", "/api/v1/missing", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 404 || bytes.Contains(rr.Body.Bytes(), []byte("<html")) {
		t.Fatalf("api miss %d %q", rr.Code, rr.Body.String())
	}
}
func TestLoginSetsSevenDaySecureCookie(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret, _, err := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "correct horse battery staple", "otp": auth.Code(secret, time.Now())})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Origin", "https://example.test")
	before := time.Now()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login status %d", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "herdr_session" || cookie.MaxAge != 7*24*60*60 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatal("session cookie attributes")
	}
	if cookie.Expires.Before(before.Add(7*24*time.Hour-time.Second)) || cookie.Expires.After(time.Now().Add(7*24*time.Hour+time.Second)) {
		t.Fatal("cookie expiry differs from seven-day lifetime")
	}
}
func TestStatusReissuesOnlyValidLegacyCookieToOriginalSevenDayExpiry(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret, _, err := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	token, _, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, at), at)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(token))
	_, err = s.Store.DB.Exec("UPDATE sessions SET expires_at=? WHERE token_hash=?", at.Add(24*time.Hour).Format(time.RFC3339Nano), hash[:])
	if err != nil {
		t.Fatal(err)
	}
	status := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
		req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		return rr
	}
	rr := status()
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Fatal("valid legacy denied")
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("valid status cookies %d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "herdr_session" || cookie.MaxAge < 6*24*60*60 || cookie.MaxAge > 7*24*60*60 || cookie.Expires.After(at.Add(7*24*time.Hour)) || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("legacy cookie not bounded to original lifetime")
	}
	if err := s.Auth.Logout(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	rr = status()
	if len(rr.Result().Cookies()) != 0 || bytes.Contains(rr.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Fatal("revoked status reissued cookie")
	}
}
func TestSpacesReachAuthenticatedSessionsAndEvents(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, spaceProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret, _, err := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, time.Now()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/sessions", "/api/v1/events"} {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
		req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
		rr := httptest.NewRecorder()
		if path == "/api/v1/events" {
			s.ServeHTTP(cancelOnWrite{rr, cancel}, req)
		} else {
			s.ServeHTTP(rr, req)
			cancel()
		}
		if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"id":"server:workspace:one"`)) {
			t.Fatalf("%s status=%d spaces missing", path, rr.Code)
		}
	}
}
func TestWebSocketRejectsUnauthenticatedBeforeUpgrade(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	req := httptest.NewRequest("GET", "/api/v1/terminals/one/stream?mode=control", nil)
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("Upgrade", "websocket")
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 401 {
		t.Fatalf("got %d", rr.Code)
	}
}
func TestStreamRequiresCurrentCapability(t *testing.T) {
	dir := t.TempDir()
	s, e := New(Config{DataDir: dir, Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	secret, _, e := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	token, _, e := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, time.Now()), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("GET", "/api/v1/terminals/missing/stream?mode=control", nil)
	req.Header.Set("Origin", "https://example.test")
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("got %d", rr.Code)
	}
}

func TestLogoutDoesNotReportSuccessWhenRevocationFails(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	secret, _, e := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now()
	token, csrf, e := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, at), at)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Store.DB.Exec(`CREATE TRIGGER fail_logout BEFORE UPDATE ON sessions BEGIN SELECT RAISE(FAIL, 'simulated write failure'); END`); e != nil {
		t.Fatal(e)
	}
	req := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 503 {
		t.Fatalf("failed revocation reported status %d", rr.Code)
	}
	if len(rr.Result().Cookies()) != 0 {
		t.Fatal("failed logout cleared the client's session cookie")
	}
	if _, e = s.Auth.Validate(context.Background(), token, time.Now()); e != nil {
		t.Fatal("test failure did not leave bearer valid")
	}
}

func TestStartupRejectsWrongKeyOrCorruptNonceWithoutChangingFiles(t *testing.T) {
	for _, variant := range []string{"wrong key", "corrupt nonce"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			config := Config{DataDir: dir, Origin: "https://example.test"}
			s, e := New(config, nil, fakeProvider{})
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = s.Auth.Setup(context.Background(), "admin", "correct horse battery staple"); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			keyPath := filepath.Join(dir, "auth.key")
			if variant == "wrong key" {
				if e = os.WriteFile(keyPath, make([]byte, 32), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				db, openErr := store.Open(filepath.Join(dir, "space.db"))
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, e = db.DB.Exec("UPDATE admin SET totp_nonce=? WHERE id=1", []byte{1})
				closeErr := db.Close()
				if e != nil || closeErr != nil {
					t.Fatalf("prepare corrupt nonce: %v, %v", e, closeErr)
				}
			}
			beforeKey, e := os.ReadFile(keyPath)
			if e != nil {
				t.Fatal(e)
			}
			started, e := New(config, nil, fakeProvider{})
			if e == nil {
				started.Close()
				t.Fatal("server started with unusable authenticator data")
			}
			afterKey, e := os.ReadFile(keyPath)
			if e != nil || !bytes.Equal(beforeKey, afterKey) {
				t.Fatalf("startup changed encryption key: %v", e)
			}
			db, e := store.Open(filepath.Join(dir, "space.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var nonce []byte
			if e = db.DB.QueryRow("SELECT totp_nonce FROM admin WHERE id=1").Scan(&nonce); e != nil {
				t.Fatal(e)
			}
			if variant == "corrupt nonce" && !bytes.Equal(nonce, []byte{1}) {
				t.Fatal("startup changed configured account")
			}
		})
	}
}
