package herdr

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSubscribeSignalsOnEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, e := net.Listen("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(c).Decode(&req)
		if req.Method != "events.subscribe" {
			return
		}
		c.Write([]byte(`{"id":"1","result":{"type":"events_subscribed"}}` + "\n" + `{"event":{"type":"pane.created"}}` + "\n"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	seen := make(chan struct{}, 1)
	_ = (Client{Socket: path}).Subscribe(ctx, func() { seen <- struct{}{} })
	select {
	case <-seen:
	case <-ctx.Done():
		t.Fatal("event ignored")
	}
}

func TestSubscribeEOFDuringReconnectDoesNotRetainGoroutines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := runtime.NumGoroutine()
	for i := 0; i < 24; i++ {
		_ = (Client{Socket: path}).Subscribe(ctx, func() {})
	}
	runtime.Gosched()
	if got := runtime.NumGoroutine(); got > before+4 {
		t.Fatalf("subscriptions retained %d goroutines", got-before)
	}
}

func TestSubscribeAcceptsNullErrorInSuccessfulResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req any
		_ = json.NewDecoder(conn).Decode(&req)
		_, _ = conn.Write([]byte(`{"id":"1","result":{"type":"events_subscribed"},"error":null}` + "\n" + `{"event":{"type":"pane.updated"}}` + "\n"))
	}()
	seen := 0
	_ = (Client{Socket: path}).Subscribe(context.Background(), func() { seen++ })
	if seen != 1 {
		t.Fatal("successful subscription with null error was rejected")
	}
}
