package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A real TCP connection is required: ResponseRecorder cannot exercise the
// server's post-handler request-body drain.
func TestBodyDeadlineBoundsSlowDecodeAndEarlyDenial(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, origin, body string
		want                             int
	}{
		{"decode", "POST", "/api/v1/auth/login", "https://example.test", `{"Username":"admin","Password":"secret"}`, 400},
		{"auth", "POST", "/api/v1/projects", "https://example.test", `{"name":"A"}`, 401},
		{"method", "PUT", "/api/v1/auth/login", "https://example.test", `{"name":"A"}`, 401},
		{"get-with-body", "GET", "/api/v1/projects", "https://example.test", `{"name":"` + strings.Repeat("A", 100) + `"}`, 401},
		{"static-method", "POST", "/", "https://example.test", `{"name":"` + strings.Repeat("A", 100) + `"}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", BodyReadTimeout: 80 * time.Millisecond}, nil, fakeProvider{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ts := httptest.NewServer(s)
			defer ts.Close()
			conn, err := net.Dial("tcp", ts.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
			start := time.Now()
			_, err = fmt.Fprintf(conn, "%s %s HTTP/1.1\r\nHost: example.test\r\nOrigin: %s\r\nContent-Length: %d\r\n\r\n%s", tc.method, tc.path, tc.origin, len(tc.body), tc.body[:1])
			if err != nil {
				t.Fatal(err)
			}
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				for i := 1; i < len(tc.body); i++ {
					select {
					case <-stop:
						return
					case <-time.After(30 * time.Millisecond):
					}
					if _, err := conn.Write([]byte(tc.body[i : i+1])); err != nil {
						return
					}
				}
			}()
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("response after %s: %v", time.Since(start), err)
			}
			defer response.Body.Close()
			if time.Since(start) > 300*time.Millisecond {
				t.Fatalf("slow body held response for %s", time.Since(start))
			}
			if response.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", response.StatusCode, tc.want)
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "/" && !strings.Contains(string(body), `"error"`) {
				t.Fatalf("non-JSON error: %q", body)
			}
		})
	}
}

func TestAPIBodyDeadlineBoundsChunkedEarlyDenial(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", BodyReadTimeout: 80 * time.Millisecond}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
	start := time.Now()
	_, err = io.WriteString(conn, "POST /api/v1/projects HTTP/1.1\r\nHost: example.test\r\nOrigin: https://example.test\r\nTransfer-Encoding: chunked\r\n\r\n1\r\n{\r\n")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("chunked denial after %s: %v", time.Since(start), err)
	}
	defer response.Body.Close()
	if time.Since(start) > 300*time.Millisecond || response.StatusCode != 401 {
		t.Fatalf("chunked denial status %d after %s", response.StatusCode, time.Since(start))
	}
}

func TestAPIBodyDeadlineKeepsStrictJSONAndSizeLimit(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", BodyReadTimeout: time.Second}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"valid", `{"Username":"admin","Password":"secret"}`, 401},
		{"unknown-field", `{"Unknown":true}`, 400},
		{"extra-json", `{} {}`, 400},
		{"over-1-mib", `{"Username":"` + strings.Repeat("A", 1<<20) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", "https://example.test")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
}

func TestAPIBodyDeadlineLeavesLongLivedGETStreamsOpen(t *testing.T) {
	s, server, provider, codes := newAuthStreamServer(t)
	s.C.BodyReadTimeout = 80 * time.Millisecond
	conn, stream, token := openAuthStream(t, s, server, provider, codes[0], "control")
	defer conn.CloseNow()
	time.Sleep(120 * time.Millisecond)
	sendStreamCommand(t, conn, "input")
	select {
	case event := <-stream.events:
		if event != "input" {
			t.Fatalf("WebSocket event %q", event)
		}
	case <-time.After(time.Second):
		t.Fatal("WebSocket GET closed after body deadline")
	}
	s.mu.Lock()
	previous := make(map[string]bool, len(s.streams[token]))
	for id := range s.streams[token] {
		previous[id] = true
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE status %d", response.StatusCode)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "event: sessions\n" {
		t.Fatalf("SSE initial event %q: %v", line, err)
	}
	s.mu.Lock()
	sseID := ""
	for id := range s.streams[token] {
		if !previous[id] {
			sseID = id
		}
	}
	s.mu.Unlock()
	if sseID == "" {
		t.Fatal("SSE stream did not register")
	}
	time.Sleep(120 * time.Millisecond)
	s.mu.Lock()
	_, active := s.streams[token][sseID]
	s.mu.Unlock()
	if !active {
		t.Fatal("SSE GET closed after body deadline")
	}
}

func TestAPIBodyDeadlineAllowsNormalKeepAlive(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", BodyReadTimeout: 80 * time.Millisecond}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewServer(s)
	defer ts.Close()
	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for i := 0; i < 2; i++ {
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		body := `{"Username":"admin","Password":"secret"}`
		_, err = fmt.Fprintf(conn, "POST /api/v1/auth/login HTTP/1.1\r\nHost: example.test\r\nOrigin: https://example.test\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 401 {
			t.Fatalf("status %d", response.StatusCode)
		}
		if i == 0 {
			time.Sleep(120 * time.Millisecond)
		}
	}
}
