// The browser fixture is a separate test executable. It is never linked into
// cmd/herdr-space and never talks to real HERDR sessions or processes.
package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"herdr-space/internal/api"
	"herdr-space/internal/auth"
	"herdr-space/internal/model"
	"herdr-space/internal/store"
	"herdr-space/internal/webassets"
)

type fixtureProvider struct {
	mu             sync.Mutex
	sessions       []model.Session
	stopped        int
	projectPath    string
	terminalDelay  time.Duration
	terminalLoad   bool
	terminalFrames atomic.Int64
}

func (p *fixtureProvider) Inventory(context.Context) (model.Inventory, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := append([]model.Session{}, p.sessions...)
	return model.Inventory{Items: items, Spaces: []model.Space{{ID: "fixture:workspace:one", Name: "Workspace", Path: p.projectPath, ServerID: "fixture", WorkspaceID: "one"}, {ID: "fixture:workspace:two", Name: "Research", Path: "", ServerID: "fixture", WorkspaceID: "two"}}}, nil
}

func (p *fixtureProvider) Stop(_ context.Context, id string, _ bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.sessions {
		if p.sessions[i].ID == id {
			p.sessions[i].Alive = false
			p.sessions[i].Capabilities = []string{}
			p.stopped++
			return nil
		}
	}
	return errors.New("session not found")
}

func (p *fixtureProvider) Resume(ctx context.Context, _ string, req model.StartRequest) (model.Session, error) {
	return p.Launch(ctx, req)
}
func (p *fixtureProvider) Launch(_ context.Context, req model.StartRequest) (model.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := model.Session{ID: "fixture-" + randomString(), Name: "Test agent", Source: "herdr", Agent: req.Agent, Launcher: req.Launcher, ProjectID: req.ProjectID, CWD: req.ProjectPath, ServerID: "fixture", TerminalID: randomString(), WorkspaceID: "one", Membership: "managed", Activity: "idle", Alive: true, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Capabilities: []string{"observe", "control", "stop"}}
	p.sessions = append(p.sessions, s)
	return s, nil
}

func (p *fixtureProvider) Attach(ctx context.Context, r model.StreamRequest) (model.TerminalStream, error) {
	inv, _ := p.Inventory(ctx)
	found := false
	for _, s := range inv.Items {
		if s.ID == r.ID && s.Alive {
			found = true
		}
	}
	if !found {
		return nil, errors.New("terminal unavailable")
	}
	s := &fixtureStream{frames: make(chan []byte, 32), errs: make(chan error, 1), done: make(chan struct{}), mode: r.Mode, provider: p}
	frame := p.frame(r.Rows)
	if p.terminalLoad {
		go func() {
			timer := time.NewTimer(p.terminalDelay)
			defer timer.Stop()
			select {
			case <-timer.C:
				select {
				case s.frames <- frame:
					s.initialDelivered.Store(true)
				case <-s.done:
				}
			case <-s.done:
			}
		}()
	} else {
		s.frames <- frame
		s.initialDelivered.Store(true)
	}
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}

type fixtureStream struct {
	frames           chan []byte
	errs             chan error
	done             chan struct{}
	once             sync.Once
	mode             string
	provider         *fixtureProvider
	initialDelivered atomic.Bool
}

func (p *fixtureProvider) frame(rows int) []byte {
	frame := fixtureScreen(rows)
	if p.terminalLoad {
		sequence := p.terminalFrames.Add(1)
		return []byte(strings.Replace(string(frame), "FINAL_PROMPT >", fmt.Sprintf("FRAME_%06d FINAL_PROMPT >", sequence), 1))
	}
	return frame
}

func fixtureScreen(rows int) []byte {
	if rows < 2 {
		rows = 2
	}
	var screen strings.Builder
	screen.WriteString("\x1b[2J\x1b[H\x1b[32mHERDR Space • test terminal\x1b[0m\r\n")
	for line := 2; line < rows; line++ {
		fmt.Fprintf(&screen, "Test line %d\r\n", line)
	}
	screen.WriteString("FINAL_PROMPT > ")
	return []byte(screen.String())
}

func (s *fixtureStream) Frames() <-chan []byte { return s.frames }
func (s *fixtureStream) Errors() <-chan error  { return s.errs }
func (s *fixtureStream) Input(b []byte) error {
	if s.mode != "control" {
		return errors.New("observe only")
	}
	select {
	case <-s.done:
		return errors.New("closed")
	case s.frames <- append([]byte{}, b...):
		return nil
	}
}
func (s *fixtureStream) Resize(_ int, rows int) error {
	if s.mode != "control" {
		return errors.New("observe only")
	}
	if s.provider.terminalLoad && !s.initialDelivered.Load() {
		return nil // Synthetic load gate: early resize must not bypass first-frame delay.
	}
	select {
	case <-s.done:
		return errors.New("closed")
	case s.frames <- s.provider.frame(rows):
		return nil
	}
}
func (s *fixtureStream) Scroll(int) error {
	if s.mode != "control" {
		return errors.New("observe only")
	}
	return nil
}
func (s *fixtureStream) Close() error { s.once.Do(func() { close(s.done) }); return nil }
func randomString() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}

func main() {
	bootstrap := flag.Bool("bootstrap", false, "test first-run enrollment with a signed fixture Access JWT")
	legacySW := flag.Bool("legacy-sw", false, "start with an isolated legacy service worker for migration tests")
	terminalDelayMS := flag.Int("terminal-frame-delay-ms", 0, "synthetic first terminal frame delay for opt-in load tests")
	terminalLoad := flag.Bool("terminal-load", false, "tag synthetic terminal frames for opt-in load tests")
	flag.Parse()
	if *terminalDelayMS < 0 || *terminalDelayMS > 5000 {
		panic("invalid synthetic terminal delay")
	}
	dir, e := os.MkdirTemp("", "herdr-space-browser-")
	if e != nil {
		panic(e)
	}
	defer os.RemoveAll(dir)
	projectDir := filepath.Join(dir, "project")
	if e = os.Mkdir(projectDir, 0700); e != nil {
		panic(e)
	}
	// Local-only repository metadata for Space discovery. GitHub transport is
	// injected below; these commands never contact a remote server.
	for _, args := range [][]string{{"init", "--quiet", projectDir}, {"-C", projectDir, "config", "remote.origin.url", "https://github.com/herdr-fixture/workspace.git"}} {
		if e = exec.Command("git", args...).Run(); e != nil {
			panic("failed to prepare isolated repository")
		}
	}
	s, e := store.Open(filepath.Join(dir, "space.db"))
	if e != nil {
		panic(e)
	}
	a, e := auth.New(s, filepath.Join(dir, "auth.key"))
	if e != nil {
		panic(e)
	}
	username, password := "browser-test", randomString()+randomString()
	secret := ""
	if !*bootstrap {
		secret, _, e = a.Setup(context.Background(), username, password)
		if e != nil {
			panic(e)
		}
	}
	if e = s.DB.Close(); e != nil {
		panic(e)
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		panic(e)
	}
	url := "http://" + l.Addr().String()
	provider := &fixtureProvider{projectPath: projectDir, sessions: []model.Session{{ID: "fixture-terminal", Name: "Codex · isolated test", Source: "herdr", Agent: "codex", Launcher: "codex", ServerID: "fixture", TerminalID: "term_fixture", WorkspaceID: "one", Membership: "managed", Activity: "working", Alive: true, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Capabilities: []string{"observe", "control", "stop"}}}}
	provider.terminalDelay = time.Duration(*terminalDelayMS) * time.Millisecond
	provider.terminalLoad = *terminalLoad
	for index, name := range []string{"Review", "Research", "Planning", "Validation", "Integration"} {
		session := provider.sessions[0]
		session.ID = fmt.Sprintf("fixture-terminal-%d", index+2)
		session.TerminalID = fmt.Sprintf("term_fixture_%d", index+2)
		session.Name = name
		session.Activity = []string{"idle", "done", "unknown", "error", "ended"}[index]
		if index == 4 {
			session.Alive = false
			session.Activity = "ended"
			session.Capabilities = []string{}
		}
		provider.sessions = append(provider.sessions, session)
	}
	config := api.Config{DataDir: dir, Origin: url, AllowInsecureLocal: true, ProjectRoots: []string{projectDir}, GitHub: newFixtureGitHub()}
	accessToken := ""
	if *bootstrap {
		config.BootstrapIssuer = "https://fixture.cloudflareaccess.com"
		config.BootstrapAudience = "fixture-application"
		config.BootstrapEmail = "owner@fixture.test"
		config.BootstrapHTTPClient, accessToken = accessFixture(config.BootstrapIssuer, config.BootstrapAudience, config.BootstrapEmail)
	}
	app, e := api.New(config, webassets.FS(), provider)
	if e != nil {
		panic(e)
	}
	defer app.Close()
	var migrated atomic.Bool
	handler := http.Handler(app)
	if *legacySW {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/__fixture/migrate" && r.Method == http.MethodPost {
				migrated.Store(true)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if r.URL.Path == "/legacy.html" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				_, _ = io.WriteString(w, `<!doctype html><title>Force Agent</title><h1>Force Agent</h1><label>Draft <textarea></textarea></label>`)
				return
			}
			if !migrated.Load() {
				switch r.URL.Path {
				case "/sw.js":
					w.Header().Set("Content-Type", "application/javascript")
					w.Header().Set("Cache-Control", "no-store")
					_, _ = io.WriteString(w, `self.addEventListener('install', event => event.waitUntil(caches.open('workbox-precache-v2-' + self.registration.scope).then(cache => cache.add('/legacy.html')))); self.addEventListener('activate', event => event.waitUntil(self.clients.claim())); self.addEventListener('fetch', event => { if (event.request.mode === 'navigate' && new URL(event.request.url).pathname !== '/__fixture/network') event.respondWith(caches.match('/legacy.html')); });`)
					return
				case "/", "/index.html":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.Header().Set("Cache-Control", "no-store")
					_, _ = io.WriteString(w, `<!doctype html><title>Force Agent</title><h1>Force Agent</h1><label>Draft <textarea></textarea></label>`)
					return
				}
			}
			app.ServeHTTP(w, r)
		})
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	srv.RegisterOnShutdown(app.BeginShutdown)
	go srv.Serve(l)
	// Only the Node fixture controller consumes stdout. Never forward it to logs.
	json.NewEncoder(os.Stdout).Encode(map[string]string{"url": url, "username": username, "password": password, "totp_secret": secret, "project_path": projectDir, "access_jwt": accessToken})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdown)
}

type fixtureKeys struct{ body, endpoint string }

func (f fixtureKeys) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() != f.endpoint {
		return nil, errors.New("unexpected fixture key request")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(f.body)), Request: request}, nil
}

func accessFixture(issuer, audience, email string) (*http.Client, string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic("fixture key generation failed")
	}
	encode := func(value any) string {
		bytes, err := json.Marshal(value)
		if err != nil {
			panic("fixture encoding failed")
		}
		return base64.RawURLEncoding.EncodeToString(bytes)
	}
	jwks, _ := json.Marshal(map[string]any{"keys": []any{map[string]string{"kid": "fixture-key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
	now := time.Now().Unix()
	unsigned := encode(map[string]string{"alg": "RS256", "kid": "fixture-key"}) + "." + encode(map[string]any{"iss": issuer, "aud": []string{audience}, "email": email, "sub": "fixture-owner", "type": "app", "exp": now + 3600, "nbf": now - 5, "iat": now - 5})
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		panic("fixture signing failed")
	}
	return &http.Client{Transport: fixtureKeys{body: string(jwks), endpoint: issuer + "/cdn-cgi/access/certs"}}, unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}
