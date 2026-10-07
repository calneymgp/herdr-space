package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"herdr-space/internal/auth"
)

func TestBeginShutdownDrainsActiveSSEBeforeStoreCloses(t *testing.T) {
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secret, _, err := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	token, _, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, at), at)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(s)
	server.Config.RegisterOnShutdown(s.BeginShutdown)
	server.Start()
	defer server.Close()
	req, err := http.NewRequest("GET", server.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE status %d", response.StatusCode)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "event: sessions") {
		t.Fatalf("SSE did not become active: %q %v", line, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Config.Shutdown(ctx); err != nil {
		t.Fatalf("active SSE blocked graceful shutdown: %v", err)
	}
	if configured, _, err := s.Auth.Configured(context.Background()); err != nil || !configured {
		t.Fatalf("store closed before Server.Close: configured=%v error=%v", configured, err)
	}
	canceled := make(chan struct{})
	if id := s.addStream(token, func() { close(canceled) }); id != "" {
		t.Fatal("stream registered after shutdown began")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("late stream was not canceled")
	}
}
