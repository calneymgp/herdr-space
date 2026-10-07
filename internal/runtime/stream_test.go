package runtime

import (
	"context"
	"errors"
	"herdr-space/internal/model"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAttachRejectsInvalidViewportBeforeLaunchingCLI(t *testing.T) {
	m, e := New(Config{HerdrBinary: filepath.Join(t.TempDir(), "nonexistent")})
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.Attach(context.Background(), model.StreamRequest{ID: "x", Mode: "control", Cols: 0, Rows: 40})
	if e == nil {
		t.Fatal("invalid viewport accepted")
	}
	m.Close()
}
func TestLaunchRejectsLauncherOutsideAllowlist(t *testing.T) {
	m, _ := New(Config{})
	_, e := m.Launch(context.Background(), model.StartRequest{Agent: "codex", Launcher: "sh", ProjectPath: os.TempDir()})
	if e == nil {
		t.Fatal("shell launcher accepted")
	}
	m.Close()
}
func TestStreamDecodesBase64ANSIFrame(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf '%s\n' '{"type":"terminal.frame","bytes":"G1szMm0="}' '{"type":"terminal.closed"}'`)
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &stream{mode: "observe", cmd: cmd, frames: make(chan []byte, 2), errs: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
	go s.read(out)
	got := <-s.Frames()
	if string(got) != "\x1b[32m" {
		t.Fatalf("frame bytes: %q", got)
	}
	<-s.done
	_ = ctx
}

func TestStreamReportsSlowConsumerAndBridgeFailureSentinels(t *testing.T) {
	t.Run("slow consumer", func(t *testing.T) {
		cmd := exec.Command("sh", "-c", `printf '%s\n' '{"type":"terminal.frame","bytes":"QQ=="}' '{"type":"terminal.frame","bytes":"Qg=="}' '{"type":"terminal.closed"}'`)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &stream{mode: "observe", cmd: cmd, frames: make(chan []byte, 1), errs: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
		go s.read(out)
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Fatal("terminal stream did not finish")
		}
		if frame := <-s.frames; string(frame) != "A" {
			t.Fatalf("first pending frame = %q, want A", frame)
		}
		if err := <-s.errs; !errors.Is(err, model.ErrTerminalStreamSlowConsumer) {
			t.Fatalf("slow consumer error = %v, want safe sentinel", err)
		}
		_ = ctx
	})

	t.Run("bridge failure", func(t *testing.T) {
		cmd := exec.Command("sh", "-c", `printf '%s\n' 'not-json'`)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &stream{mode: "observe", cmd: cmd, frames: make(chan []byte, 1), errs: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
		go s.read(out)
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Fatal("terminal stream did not finish")
		}
		if err := <-s.errs; !errors.Is(err, model.ErrTerminalStreamBridge) {
			t.Fatalf("bridge error = %v, want safe sentinel", err)
		}
		_ = ctx
	})

	t.Run("unexpected EOF", func(t *testing.T) {
		cmd := exec.Command("sh", "-c", `printf '%s\n' '{"type":"terminal.frame","bytes":"QQ=="}'`)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &stream{mode: "observe", cmd: cmd, frames: make(chan []byte, 1), errs: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
		go s.read(out)
		select {
		case <-s.done:
		case <-time.After(time.Second):
			t.Fatal("terminal stream did not finish")
		}
		if frame := <-s.frames; string(frame) != "A" {
			t.Fatalf("pending frame = %q, want A", frame)
		}
		if err := <-s.errs; !errors.Is(err, model.ErrTerminalStreamBridge) {
			t.Fatalf("unexpected EOF error = %v, want bridge failure", err)
		}
	})
}

type blockingWriter struct{ release chan struct{} }

func (w *blockingWriter) Write(p []byte) (int, error) { <-w.release; return 0, errors.New("closed") }
func (w *blockingWriter) Close() error {
	select {
	case <-w.release:
	default:
		close(w.release)
	}
	return nil
}
func TestCloseDoesNotWaitForeverForBlockedReleaseWrite(t *testing.T) {
	w := &blockingWriter{release: make(chan struct{})}
	done := make(chan struct{})
	s := &stream{mode: "control", stdin: w, done: done, cancel: func() { close(done) }}
	finished := make(chan struct{})
	go func() { _ = s.Close(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		w.Close()
		t.Fatal("Close blocked on terminal.release write")
	}
}
func TestMalformedFrameCancelsOwnedBridgeProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf '%s\n' 'not-json'; sleep 10`)
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &stream{mode: "observe", cmd: cmd, frames: make(chan []byte, 1), errs: make(chan error, 1), done: make(chan struct{}), cancel: func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}}
	go s.read(out)
	select {
	case <-s.done:
	case <-time.After(time.Second):
		s.cancel()
		t.Fatal("bridge child survived malformed frame")
	}
	_ = ctx
}
