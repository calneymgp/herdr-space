package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"herdr-space/internal/model"
)

func TestWebSocketReportsBufferedStreamErrorAfterPendingFrame(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		code    string
		message string
	}{
		{name: "bridge failure", err: fmt.Errorf("PRIVATE fixture \x1b[31m session-id test-terminal: %w", model.ErrTerminalStreamBridge), code: "terminal_unavailable", message: "The terminal stream was interrupted."},
		{name: "slow consumer", err: fmt.Errorf("PRIVATE fixture session-id test-terminal: %w", model.ErrTerminalStreamSlowConsumer), code: "terminal_slow_consumer", message: "The terminal is receiving data too quickly. Reconnect to continue."},
		{name: "unknown failure", err: errors.New("PRIVATE fixture \x1b[31m session-id test-terminal"), code: "terminal_unavailable", message: "The terminal stream was interrupted."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, server, provider, codes := newAuthStreamServer(t)
			conn, stream, _ := openAuthStream(t, s, server, provider, codes[0], "observe")
			defer conn.CloseNow()

			frame := []byte("\x1b[31mvisible terminal frame")
			stream.frames <- frame
			stream.errs <- tc.err
			close(stream.frames)
			close(stream.errs)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			typ, gotFrame, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("pending terminal frame was lost: %v", err)
			}
			if typ != websocket.MessageBinary || string(gotFrame) != string(frame) {
				t.Fatalf("first message = (%v, %q), want pending binary frame", typ, gotFrame)
			}
			typ, payload, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("public stream error was lost: %v", err)
			}
			if typ != websocket.MessageText {
				t.Fatalf("stream error type %v, want text", typ)
			}
			var message struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Fatalf("decode public stream error: %v", err)
			}
			if message.Type != "error" || message.Code != tc.code || message.Message != tc.message {
				t.Fatalf("unexpected public stream error: %+v", message)
			}
			for _, private := range []string{"PRIVATE fixture", "session-id", "test-terminal", "\x1b[31m"} {
				if strings.Contains(string(payload), private) {
					t.Fatalf("public error leaked %q: %s", private, payload)
				}
			}
			if _, _, err := conn.Read(ctx); err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("stream did not close after public error: %v", err)
			}
		})
	}
}

func TestWebSocketNormalStreamEndDoesNotReportFailure(t *testing.T) {
	s, server, provider, codes := newAuthStreamServer(t)
	conn, stream, _ := openAuthStream(t, s, server, provider, codes[0], "observe")
	defer conn.CloseNow()
	close(stream.frames)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if typ, payload, err := conn.Read(ctx); err == nil {
		t.Fatalf("normal stream end emitted message type %v: %q", typ, payload)
	} else if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("normal stream end did not close promptly: %v", err)
	}
}

func TestWebSocketErrorWithUnbufferedFramesDoesNotWaitForFrame(t *testing.T) {
	s, server, provider, codes := newAuthStreamServer(t)
	provider.frameCapacity = 0
	conn, stream, _ := openAuthStream(t, s, server, provider, codes[0], "observe")
	defer conn.CloseNow()
	stream.errs <- model.ErrTerminalStreamBridge

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("stream error waited for an unavailable frame: %v", err)
	}
	if typ != websocket.MessageText || !strings.Contains(string(payload), `"code":"terminal_unavailable"`) {
		t.Fatalf("missing public error: type=%v payload=%s", typ, payload)
	}
}

func TestWebSocketRejectsOversizedFrameWithPublicEnglishError(t *testing.T) {
	s, server, provider, codes := newAuthStreamServer(t)
	conn, stream, _ := openAuthStream(t, s, server, provider, codes[0], "observe")
	defer conn.CloseNow()
	stream.frames <- make([]byte, (1<<20)+1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var message struct{ Type, Code, Message string }
	if typ != websocket.MessageText || json.Unmarshal(payload, &message) != nil || message.Type != "error" || message.Code != "terminal_unavailable" || message.Message != "The terminal stream was interrupted." {
		t.Fatalf("oversized frame exposed or wrong public error: type=%v code=%q message=%q", typ, message.Code, message.Message)
	}
}
