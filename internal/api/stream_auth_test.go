package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdr-space/internal/model"
)

type authStreamProvider struct {
	mu            sync.Mutex
	stream        *authTestStream
	attachStarted chan struct{}
	attachDone    chan struct{}
	blockAttach   bool
	attachOnce    sync.Once
	frameCapacity int
}

func (p *authStreamProvider) Inventory(context.Context) (model.Inventory, error) {
	return model.Inventory{Items: []model.Session{{ID: "test-terminal", Alive: true, Capabilities: []string{"observe", "control"}}}}, nil
}
func (*authStreamProvider) Stop(context.Context, string, bool) error { return nil }
func (*authStreamProvider) Resume(context.Context, string, model.StartRequest) (model.Session, error) {
	return model.Session{}, nil
}
func (*authStreamProvider) Launch(context.Context, model.StartRequest) (model.Session, error) {
	return model.Session{}, nil
}
func (p *authStreamProvider) Attach(ctx context.Context, _ model.StreamRequest) (model.TerminalStream, error) {
	p.mu.Lock()
	started, block := p.attachStarted, p.blockAttach
	p.mu.Unlock()
	if started != nil {
		p.attachOnce.Do(func() { close(started) })
	}
	if block {
		<-ctx.Done()
		if p.attachDone != nil {
			close(p.attachDone)
		}
		return nil, ctx.Err()
	}
	s := &authTestStream{frames: make(chan []byte, p.frameCapacity), errs: make(chan error, 2), events: make(chan string, 4), closed: make(chan struct{})}
	p.mu.Lock()
	p.stream = s
	p.mu.Unlock()
	return s, nil
}
func (p *authStreamProvider) current() *authTestStream {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stream
}

type authTestStream struct {
	frames chan []byte
	errs   chan error
	events chan string
	closed chan struct{}
	once   sync.Once
}

func (s *authTestStream) Frames() <-chan []byte { return s.frames }
func (s *authTestStream) Errors() <-chan error  { return s.errs }
func (s *authTestStream) Input([]byte) error {
	s.events <- "input"
	return nil
}
func (s *authTestStream) Resize(int, int) error {
	s.events <- "resize"
	return nil
}
func (s *authTestStream) Scroll(int) error {
	s.events <- "scroll"
	return nil
}
func (s *authTestStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func newAuthStreamServer(t *testing.T) (*Server, *httptest.Server, *authStreamProvider, []string) {
	t.Helper()
	p := &authStreamProvider{frameCapacity: 4}
	s, e := New(Config{DataDir: t.TempDir(), Origin: "http://127.0.0.1", AllowInsecureLocal: true}, nil, p)
	if e != nil {
		t.Fatal(e)
	}
	secret, codes, e := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil || secret == "" {
		s.Close()
		t.Fatal(e)
	}
	httpServer := httptest.NewServer(s)
	s.C.Origin = httpServer.URL // Set before any test request; the port is assigned by httptest.
	t.Cleanup(func() { httpServer.Close(); s.Close() })
	return s, httpServer, p, codes
}

func openAuthStream(t *testing.T, s *Server, server *httptest.Server, p *authStreamProvider, code, mode string) (*websocket.Conn, *authTestStream, string) {
	t.Helper()
	token, csrf, e := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", code, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	conn := dialAuthWebSocket(t, server, token, mode)
	hello, _ := json.Marshal(map[string]string{"type": "hello", "csrf_token": csrf})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e := conn.Write(ctx, websocket.MessageText, hello); e != nil {
		conn.CloseNow()
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for p.current() == nil {
		if time.Now().After(deadline) {
			conn.CloseNow()
			t.Fatal("stream was not attached")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitAuthStreamRegistration(t, s, token, true)
	return conn, p.current(), token
}

func waitAuthStreamRegistration(t *testing.T, s *Server, token string, promoted bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		registered := false
		for _, stream := range s.streams[token] {
			if !promoted || stream.authInvalidated != nil {
				registered = true
			}
		}
		s.mu.Unlock()
		if registered {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("WebSocket stream was not registered")
		}
		time.Sleep(time.Millisecond)
	}
}

func dialAuthWebSocket(t *testing.T, server *httptest.Server, token, mode string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/terminals/test-terminal/stream?mode=" + mode
	header := http.Header{}
	header.Set("Origin", server.URL)
	header.Set("Cookie", "herdr_session="+token)
	conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: header})
	if e != nil {
		t.Fatal(e)
	}
	return conn
}

func sendStreamCommand(t *testing.T, conn *websocket.Conn, command string) {
	t.Helper()
	data := map[string]any{"type": command}
	switch command {
	case "input":
		data["data"] = "x"
	case "resize":
		data["cols"], data["rows"] = 100, 30
	case "scroll":
		data["delta"] = 1
	}
	message, _ := json.Marshal(data)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e := conn.Write(ctx, websocket.MessageText, message); e != nil {
		t.Fatal(e)
	}
}

func requireStreamClosed(t *testing.T, conn *websocket.Conn, stream *authTestStream, expectedCode ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if len(expectedCode) != 0 {
		typ, message, e := conn.Read(ctx)
		if e != nil {
			t.Fatalf("missing public auth error before close: %v", e)
		}
		if typ != websocket.MessageText {
			t.Fatalf("auth error frame type %v, want text", typ)
		}
		var payload struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if e := json.Unmarshal(message, &payload); e != nil {
			t.Fatalf("decode public auth error: %v", e)
		}
		if payload.Type != "error" || payload.Code != expectedCode[0] || !strings.Contains(payload.Message, "session") {
			t.Fatalf("unexpected public auth error: %+v", payload)
		}
	}
	if _, _, e := conn.Read(ctx); e == nil {
		t.Fatal("stream remained readable after session invalidation")
	} else if errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatal("stream did not close within the 5-second validation poll plus allowance")
	}
	select {
	case <-stream.closed:
	case <-time.After(time.Second):
		t.Fatal("provider stream was not closed")
	}
}

func TestWebSocketInvalidSessionCannotUseControlCommands(t *testing.T) {
	for _, invalidation := range []string{"external logout", "absolute expiry"} {
		for _, command := range []string{"input", "resize", "scroll"} {
			t.Run(invalidation+"/"+command, func(t *testing.T) {
				s, server, p, codes := newAuthStreamServer(t)
				conn, stream, token := openAuthStream(t, s, server, p, codes[0], "control")
				defer conn.CloseNow()
				switch invalidation {
				case "external logout":
					if e := s.Auth.Logout(context.Background(), token); e != nil {
						t.Fatal(e)
					}
				case "absolute expiry":
					setSessionTime(t, s, token, "expires_at", time.Now().Add(-time.Second))
				}
				// The server can close between invalidating the session and this
				// best-effort command; either way, it must not reach the provider.
				commandData := map[string]any{"type": command}
				switch command {
				case "input":
					commandData["data"] = "x"
				case "resize":
					commandData["cols"], commandData["rows"] = 100, 30
				case "scroll":
					commandData["delta"] = 1
				}
				message, _ := json.Marshal(commandData)
				writeCtx, writeCancel := context.WithTimeout(context.Background(), time.Second)
				_ = conn.Write(writeCtx, websocket.MessageText, message)
				writeCancel()
				requireStreamClosed(t, conn, stream, "auth_expired_or_revoked")
				select {
				case action := <-stream.events:
					t.Fatalf("invalid session performed %s", action)
				default:
				}
			})
		}
	}
}

func TestWebSocketLogoutCancellationReportsAuthErrorBeforeClose(t *testing.T) {
	s, server, p, codes := newAuthStreamServer(t)
	conn, stream, token := openAuthStream(t, s, server, p, codes[0], "observe")
	defer conn.CloseNow()

	s.mu.Lock()
	registered := len(s.streams[token])
	s.mu.Unlock()
	if registered == 0 {
		t.Fatal("WebSocket was not registered for logout cancellation")
	}
	s.cancelStreams(token)
	requireStreamClosed(t, conn, stream, "auth_expired_or_revoked")
}

func TestWebSocketLogoutReleasesBlockedWriter(t *testing.T) {
	s, server, p, codes := newAuthStreamServer(t)
	conn, stream, token := openAuthStream(t, s, server, p, codes[0], "control")
	defer conn.CloseNow()

	// A peer that stops reading can leave a network write pending. Fill the
	// stream without reading the socket, then revoke while that write is busy.
	frame := make([]byte, 1<<20)
	for i := 0; i < cap(stream.frames); i++ {
		stream.frames <- frame
	}
	deadline := time.After(2 * time.Second)
	for len(stream.frames) == cap(stream.frames) {
		select {
		case <-deadline:
			t.Fatal("server did not start writing terminal frames")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	fillUntil := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(fillUntil) {
		select {
		case stream.frames <- frame:
		default:
			time.Sleep(time.Millisecond)
		}
	}
	s.cancelStreams(token)
	select {
	case <-stream.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("revocation left the bridge open behind a blocked WebSocket write")
	}
}

func TestWebSocketPreAttachRevocationAndShutdown(t *testing.T) {
	for _, phase := range []string{"hello", "attach"} {
		for _, action := range []string{"logout", "shutdown"} {
			t.Run(phase+"/"+action, func(t *testing.T) {
				s, server, p, codes := newAuthStreamServer(t)
				token, csrf, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", codes[0], time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if phase == "attach" {
					p.blockAttach = true
					p.attachStarted = make(chan struct{})
					p.attachDone = make(chan struct{})
				}
				conn := dialAuthWebSocket(t, server, token, "observe")
				defer conn.CloseNow()
				waitAuthStreamRegistration(t, s, token, false)
				if phase == "attach" {
					hello, _ := json.Marshal(map[string]string{"type": "hello", "csrf_token": csrf})
					ctx, cancel := context.WithTimeout(context.Background(), time.Second)
					if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
						t.Fatal(err)
					}
					cancel()
					select {
					case <-p.attachStarted:
					case <-time.After(time.Second):
						t.Fatal("Attach was not entered")
					}
				}
				if action == "logout" {
					s.cancelStreams(token)
				} else {
					s.BeginShutdown()
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if _, _, err := conn.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("pre-attach socket stayed open after %s: %v", action, err)
				}
				if phase == "attach" {
					select {
					case <-p.attachDone:
					case <-time.After(time.Second):
						t.Fatal("Attach context survived cancellation")
					}
				}
			})
		}
	}
}

func setSessionTime(t *testing.T, s *Server, token, column string, at time.Time) {
	t.Helper()
	if column != "expires_at" && column != "seen_at" {
		t.Fatal("invalid test column")
	}
	h := sha256.Sum256([]byte(token))
	if _, e := s.Store.DB.Exec("UPDATE sessions SET "+column+"=? WHERE token_hash=?", at.UTC().Format(time.RFC3339Nano), h[:]); e != nil {
		t.Fatal(e)
	}
}

func TestWebSocketPassiveObservationSurvivesIdleUntilAbsoluteExpiry(t *testing.T) {
	for _, age := range []time.Duration{2 * time.Hour, 6 * 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			s, server, p, codes := newAuthStreamServer(t)
			conn, stream, token := openAuthStream(t, s, server, p, codes[0], "observe")
			defer conn.CloseNow()
			created := time.Now().UTC().Add(-age)
			h := sha256.Sum256([]byte(token))
			if _, e := s.Store.DB.Exec("UPDATE sessions SET created_at=?,seen_at=?,expires_at=? WHERE token_hash=?", created.Format(time.RFC3339Nano), created.Format(time.RFC3339Nano), created.Add(7*24*time.Hour).Format(time.RFC3339Nano), h[:]); e != nil {
				t.Fatal(e)
			}
			if _, e := s.Auth.Validate(context.Background(), token, time.Now()); e != nil {
				t.Fatalf("valid after %s: %v", age, e)
			}
			select {
			case <-stream.closed:
				t.Fatal("passive stream closed while session valid")
			case <-time.After(5200 * time.Millisecond):
			}
			select {
			case <-stream.closed:
				t.Fatal("passive stream closed on validation poll")
			default:
			}
			var seen string
			if e := s.Store.DB.QueryRow("SELECT seen_at FROM sessions WHERE token_hash=?", h[:]).Scan(&seen); e != nil {
				t.Fatal(e)
			}
			if seen != created.Format(time.RFC3339Nano) {
				t.Fatal("passive observation changed seen_at")
			}
			setSessionTime(t, s, token, "expires_at", time.Now().Add(-time.Second))
			requireStreamClosed(t, conn, stream, "auth_expired_or_revoked")
		})
	}
}

func TestWebSocketControlActivityUpdatesSeenButResizeDoesNot(t *testing.T) {
	s, server, p, codes := newAuthStreamServer(t)
	conn, stream, token := openAuthStream(t, s, server, p, codes[0], "control")
	defer conn.CloseNow()
	old := time.Now().Add(-30 * time.Minute)
	setSessionTime(t, s, token, "seen_at", old)
	h := sha256.Sum256([]byte(token))
	seen := func() string {
		var v string
		if e := s.Store.DB.QueryRow("SELECT seen_at FROM sessions WHERE token_hash=?", h[:]).Scan(&v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	sendStreamCommand(t, conn, "resize")
	select {
	case action := <-stream.events:
		if action != "resize" {
			t.Fatalf("got %s", action)
		}
	case <-time.After(time.Second):
		t.Fatal("resize was not handled")
	}
	if seen() != old.UTC().Format(time.RFC3339Nano) {
		t.Fatal("resize updated seen_at")
	}
	for _, command := range []string{"input", "scroll"} {
		setSessionTime(t, s, token, "seen_at", old)
		sendStreamCommand(t, conn, command)
		select {
		case action := <-stream.events:
			if action != command {
				t.Fatalf("got %s, want %s", action, command)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s was not handled", command)
		}
		if seen() == old.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("%s did not update seen_at", command)
		}
	}
}

func TestSSESurvivesIdleAndClosesOnAbsoluteExpiryOrRevocation(t *testing.T) {
	for _, invalidation := range []string{"absolute expiry", "revocation"} {
		t.Run(invalidation, func(t *testing.T) {
			s, server, _, codes := newAuthStreamServer(t)
			token, _, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", codes[0], time.Now())
			if err != nil {
				t.Fatal(err)
			}
			created := time.Now().UTC().Add(-6 * 24 * time.Hour)
			h := sha256.Sum256([]byte(token))
			if _, err := s.Store.DB.Exec("UPDATE sessions SET created_at=?,seen_at=?,expires_at=? WHERE token_hash=?", created.Format(time.RFC3339Nano), created.Format(time.RFC3339Nano), created.Add(7*24*time.Hour).Format(time.RFC3339Nano), h[:]); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 13*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("SSE status %d", resp.StatusCode)
			}
			reader := bufio.NewReader(resp.Body)
			line, err := reader.ReadString('\n')
			if err != nil || line != "event: sessions\n" {
				t.Fatalf("valid session missed SSE event: %v", err)
			}
			if invalidation == "absolute expiry" {
				setSessionTime(t, s, token, "expires_at", time.Now().Add(-time.Second))
			} else if err := s.Auth.Logout(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(io.Discard, reader); err != nil || ctx.Err() != nil {
				t.Fatalf("SSE did not close on %s: %v", invalidation, err)
			}
		})
	}
}

func TestBeginShutdownClosesActiveWebSocket(t *testing.T) {
	s, server, p, codes := newAuthStreamServer(t)
	conn, stream, _ := openAuthStream(t, s, server, p, codes[0], "control")
	defer conn.CloseNow()
	s.BeginShutdown()
	requireStreamClosed(t, conn, stream)
	if configured, _, err := s.Auth.Configured(context.Background()); err != nil || !configured {
		t.Fatalf("store closed before Server.Close: configured=%v error=%v", configured, err)
	}
}
