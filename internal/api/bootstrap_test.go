package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"herdr-space/internal/auth"
)

func setupRequest(method, url, accessToken string, body any) *http.Request {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, url, bytes.NewReader(data))
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("Cf-Access-Jwt-Assertion", accessToken)
	return req
}
func setupServer(t *testing.T) *Server {
	t.Helper()
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", BootstrapIssuer: "https://test.cloudflareaccess.com", BootstrapAudience: "aud", BootstrapEmail: "owner@example.test,other@example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	s.bootstrapVerify = func(_ context.Context, r *http.Request) (auth.AccessIdentity, error) {
		switch r.Header.Get("Cf-Access-Jwt-Assertion") {
		case "valid":
			return auth.AccessIdentity{Email: "owner@example.test", Subject: "user-1"}, nil
		case "other":
			return auth.AccessIdentity{Email: "other@example.test", Subject: "user-2"}, nil
		default:
			return auth.AccessIdentity{}, errors.New("invalid")
		}
	}
	return s
}
func TestBrowserSetupRequiresAccessAndCommitsOnlyAfterTOTP(t *testing.T) {
	s := setupServer(t)
	defer s.Close()
	status := httptest.NewRecorder()
	s.ServeHTTP(status, setupRequest("GET", "/api/v1/auth/status", "valid", nil))
	if !bytes.Contains(status.Body.Bytes(), []byte(`"setup_available":true`)) || !bytes.Contains(status.Body.Bytes(), []byte(`"setup_browser_enabled":true`)) {
		t.Fatalf("status %s", status.Body.String())
	}
	denied := httptest.NewRecorder()
	s.ServeHTTP(denied, setupRequest("POST", "/api/v1/auth/setup/begin", "", map[string]string{"username": "admin", "password": "correct horse battery staple"}))
	if denied.Code != 403 {
		t.Fatalf("missing Access %d", denied.Code)
	}
	begin := httptest.NewRecorder()
	s.ServeHTTP(begin, setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "admin", "password": "correct horse battery staple"}))
	if begin.Code != 200 {
		t.Fatalf("begin %d %s", begin.Code, begin.Body.String())
	}
	var challenge struct {
		Secret string `json:"secret"`
		URL    string `json:"otpauth_url"`
		CSRF   string `json:"csrf_token"`
	}
	if e := json.Unmarshal(begin.Body.Bytes(), &challenge); e != nil {
		t.Fatal(e)
	}
	if challenge.Secret == "" || challenge.CSRF == "" || challenge.URL == "" {
		t.Fatal("incomplete setup challenge")
	}
	configured, _, e := s.Auth.Configured(context.Background())
	if e != nil || configured {
		t.Fatalf("premature admin %v %v", configured, e)
	}
	cookie := begin.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatal("pending cookie not protected")
	}
	for _, c := range begin.Result().Cookies() {
		if c.Name == "herdr_session" {
			t.Fatal("session issued before TOTP")
		}
	}
	for i := 0; i < 2; i++ {
		req := setupRequest("POST", "/api/v1/auth/setup/complete", "valid", map[string]string{"otp": "000000", "csrf": challenge.CSRF})
		req.AddCookie(cookie)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != 400 {
			t.Fatalf("invalid OTP attempt %d: %d", i, rr.Code)
		}
		for _, c := range rr.Result().Cookies() {
			if c.Name == "herdr_session" {
				t.Fatal("session issued on invalid TOTP")
			}
		}
	}
	req := setupRequest("POST", "/api/v1/auth/setup/complete", "valid", map[string]string{"otp": auth.Code(challenge.Secret, time.Now()), "csrf": challenge.CSRF})
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("complete %d %s", rr.Code, rr.Body.String())
	}
	var completed struct {
		Username   string   `json:"username"`
		Recovery   []string `json:"recovery_codes"`
		Configured bool     `json:"configured"`
	}
	if e := json.Unmarshal(rr.Body.Bytes(), &completed); e != nil {
		t.Fatal(e)
	}
	if !completed.Configured || completed.Username != "admin" || len(completed.Recovery) != 10 {
		t.Fatal("incomplete setup completion")
	}
	configured, _, e = s.Auth.Configured(context.Background())
	if e != nil || !configured {
		t.Fatalf("admin absent %v %v", configured, e)
	}
	replay := httptest.NewRecorder()
	s.ServeHTTP(replay, setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "other", "password": "correct horse battery staple"}))
	if replay.Code != 404 {
		t.Fatalf("configured setup %d", replay.Code)
	}
}
func TestBrowserSetupCSRFAndIdentityBinding(t *testing.T) {
	s := setupServer(t)
	defer s.Close()
	begin := httptest.NewRecorder()
	s.ServeHTTP(begin, setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "admin", "password": "correct horse battery staple"}))
	if begin.Code != 200 {
		t.Fatal(begin.Code)
	}
	var challenge struct {
		Secret string `json:"secret"`
		CSRF   string `json:"csrf_token"`
	}
	json.Unmarshal(begin.Body.Bytes(), &challenge)
	cookie := begin.Result().Cookies()[0]
	req := setupRequest("POST", "/api/v1/auth/setup/complete", "valid", map[string]string{"otp": auth.Code(challenge.Secret, time.Now()), "csrf": "wrong"})
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("csrf %d", rr.Code)
	}
	req = setupRequest("POST", "/api/v1/auth/setup/complete", "other", map[string]string{"otp": auth.Code(challenge.Secret, time.Now()), "csrf": challenge.CSRF})
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Fatalf("identity %d", rr.Code)
	}
	req = setupRequest("POST", "/api/v1/auth/setup/complete", "valid", map[string]string{"otp": auth.Code(challenge.Secret, time.Now()), "csrf": challenge.CSRF})
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 410 {
		t.Fatalf("consumed pending %d", rr.Code)
	}
	configured, _, e := s.Auth.Configured(context.Background())
	if e != nil || configured {
		t.Fatalf("admin from invalid setup %v %v", configured, e)
	}
}
func TestConcurrentDistinctPendingSetupsCreateOneAdmin(t *testing.T) {
	s := setupServer(t)
	defer s.Close()
	type pending struct {
		Secret string `json:"secret"`
		CSRF   string `json:"csrf_token"`
		Cookie *http.Cookie
		Access string
	}
	attempts := []pending{}
	for _, access := range []string{"valid", "other"} {
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, setupRequest("POST", "/api/v1/auth/setup/begin", access, map[string]string{"username": access, "password": "correct horse battery staple"}))
		if rr.Code != 200 {
			t.Fatalf("begin %s: %d", access, rr.Code)
		}
		var p pending
		json.Unmarshal(rr.Body.Bytes(), &p)
		p.Cookie = rr.Result().Cookies()[0]
		p.Access = access
		attempts = append(attempts, p)
	}
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, p := range attempts {
		wg.Add(1)
		go func(i int, p pending) {
			defer wg.Done()
			req := setupRequest("POST", "/api/v1/auth/setup/complete", p.Access, map[string]string{"otp": auth.Code(p.Secret, time.Now()), "csrf": p.CSRF})
			req.AddCookie(p.Cookie)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			statuses[i] = rr.Code
		}(i, p)
	}
	wg.Wait()
	successes := 0
	for _, status := range statuses {
		if status == 200 {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent statuses %v", statuses)
	}
	var admins, recovery int
	s.Store.DB.QueryRow("SELECT count(*) FROM admin").Scan(&admins)
	s.Store.DB.QueryRow("SELECT count(*) FROM recovery").Scan(&recovery)
	if admins != 1 || recovery != 10 {
		t.Fatalf("admins=%d recovery=%d", admins, recovery)
	}
}
func TestSetupDisabledAndConfiguredRoutesStayClosed(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, setupRequest("GET", "/api/v1/auth/status", "valid", nil))
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"setup_available":false`)) || !bytes.Contains(rr.Body.Bytes(), []byte(`"setup_browser_enabled":false`)) {
		t.Fatalf("status %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "admin", "password": "correct horse battery staple"}))
	if rr.Code != 404 {
		t.Fatalf("disabled setup %d", rr.Code)
	}
}
func TestSetupExpiresAndLimitsOTPAttempts(t *testing.T) {
	s := setupServer(t)
	defer s.Close()
	begin := httptest.NewRecorder()
	s.ServeHTTP(begin, setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "admin", "password": "correct horse battery staple"}))
	if begin.Code != 200 {
		t.Fatal(begin.Code)
	}
	var challenge struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(begin.Body.Bytes(), &challenge)
	cookie := begin.Result().Cookies()[0]
	for i := 0; i < 5; i++ {
		req := setupRequest("POST", "/api/v1/auth/setup/complete", "valid", map[string]string{"otp": "000000", "csrf": challenge.CSRF})
		req.AddCookie(cookie)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		want := 400
		if i == 4 {
			want = 410
		}
		if rr.Code != want {
			t.Fatalf("attempt %d code %d", i, rr.Code)
		}
	}
	configured, _, e := s.Auth.Configured(context.Background())
	if e != nil || configured {
		t.Fatalf("account after failed OTP %v %v", configured, e)
	}
}
func TestLocalBootstrapCookieOnlyWithLoopbackMode(t *testing.T) {
	c := Config{DataDir: t.TempDir(), Origin: "http://127.0.0.1:9380", AllowInsecureLocal: true, BootstrapIssuer: "https://test.cloudflareaccess.com", BootstrapAudience: "aud", BootstrapEmail: "owner@example.test"}
	s, e := New(c, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.bootstrapVerify = func(context.Context, *http.Request) (auth.AccessIdentity, error) {
		return auth.AccessIdentity{Email: "owner@example.test", Subject: "user-1"}, nil
	}
	req := setupRequest("POST", "/api/v1/auth/setup/begin", "valid", map[string]string{"username": "admin", "password": "correct horse battery staple"})
	req.Header.Set("Origin", c.Origin)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("begin %d", rr.Code)
	}
	if rr.Result().Cookies()[0].Secure {
		t.Fatal("local HTTP pending cookie marked Secure")
	}
	c.DataDir = t.TempDir()
	c.Origin = "http://example.com"
	if invalid, e := New(c, nil, fakeProvider{}); e == nil {
		invalid.Close()
		t.Fatal("remote insecure origin accepted")
	}
}
