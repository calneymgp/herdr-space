package runtime

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"herdr-space/internal/model"
)

const maxFrameLine = 4 << 20

type stream struct {
	mode   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	frames chan []byte
	errs   chan error
	done   chan struct{}
	cancel context.CancelFunc
	mu     sync.Mutex
	once   sync.Once
}

func (s *stream) Frames() <-chan []byte { return s.frames }
func (s *stream) Errors() <-chan error  { return s.errs }
func (s *stream) send(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return errors.New("terminal disconnected")
	default:
	}
	return json.NewEncoder(s.stdin).Encode(v)
}
func (s *stream) Input(b []byte) error {
	if s.mode != "control" {
		return errors.New("terminal is read-only")
	}
	if len(b) > 16<<10 {
		return errors.New("input is too large")
	}
	return s.send(map[string]any{"type": "terminal.input", "bytes": base64.StdEncoding.EncodeToString(b)})
}
func (s *stream) Resize(c, r int) error {
	if s.mode != "control" {
		return errors.New("terminal is read-only")
	}
	if c < 20 || c > 500 || r < 5 || r > 200 {
		return errors.New("invalid dimensions")
	}
	return s.send(map[string]any{"type": "terminal.resize", "cols": c, "rows": r})
}
func (s *stream) Scroll(delta int) error {
	if s.mode != "control" {
		return errors.New("terminal is read-only")
	}
	if delta == 0 || delta > 1000 || delta < -1000 {
		return errors.New("invalid scroll")
	}
	direction := "down"
	if delta < 0 {
		direction = "up"
		delta = -delta
	}
	return s.send(map[string]any{"type": "terminal.scroll", "direction": direction, "lines": delta})
}
func (s *stream) Close() error {
	s.once.Do(func() {
		if s.mode == "control" {
			released := make(chan struct{})
			go func() { _ = s.send(map[string]any{"type": "terminal.release"}); close(released) }()
			select {
			case <-released:
			case <-time.After(100 * time.Millisecond):
			}
		}
		s.cancel()
		_ = s.stdin.Close()
	})
	select {
	case <-s.done:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("terminal bridge did not close")
	}
}
func (m *Manager) Attach(ctx context.Context, r model.StreamRequest) (model.TerminalStream, error) {
	if r.Mode != "observe" && r.Mode != "control" {
		return nil, errors.New("invalid mode")
	}
	if r.Cols < 20 || r.Cols > 500 || r.Rows < 5 || r.Rows > 200 {
		return nil, errors.New("invalid dimensions")
	}
	if r.Takeover && r.Mode != "control" {
		return nil, errors.New("takeover requires control")
	}
	e, err := m.current(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	allowed := false
	for _, capability := range e.session.Capabilities {
		if capability == r.Mode {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, errors.New("terminal capability unavailable")
	}
	if e.session.TerminalID == "" {
		return nil, errors.New("terminal not found")
	}
	mode := r.Mode
	args := []string{"terminal", "session", mode, e.session.TerminalID, "--cols", fmt.Sprint(r.Cols), "--rows", fmt.Sprint(r.Rows)}
	if r.Takeover {
		args = append(args, "--takeover")
	}
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, m.cfg.HerdrBinary, args...)
	cmd.Env = append(os.Environ(), "HERDR_SOCKET_PATH="+e.socket)
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("HERDR stream unavailable: %w", err)
	}
	s := &stream{mode: mode, cmd: cmd, stdin: in, frames: make(chan []byte, 16), errs: make(chan error, 1), done: make(chan struct{}), cancel: cancel}
	go s.read(out)
	return s, nil
}
func (s *stream) read(out io.Reader) {
	defer close(s.done)
	defer close(s.frames)
	defer close(s.errs)
	defer func() { s.cancel(); _ = s.cmd.Wait() }()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), maxFrameLine)
	for sc.Scan() {
		var v struct {
			Type  string `json:"type"`
			Bytes string `json:"bytes"`
		}
		if json.Unmarshal(sc.Bytes(), &v) != nil {
			s.report(model.ErrTerminalStreamBridge)
			return
		}
		switch v.Type {
		case "terminal.frame":
			b, e := base64.StdEncoding.DecodeString(v.Bytes)
			if e != nil || len(b) > 2<<20 {
				s.report(model.ErrTerminalStreamBridge)
				return
			}
			select {
			case s.frames <- b:
			default:
				s.report(model.ErrTerminalStreamSlowConsumer)
			}
		case "terminal.closed":
			return
		}
	}
	// A clean bridge exit must send terminal.closed. EOF without that event
	// means the stream ended before its protocol completed.
	s.report(model.ErrTerminalStreamBridge)
}

func (s *stream) report(err error) {
	select {
	case s.errs <- err:
	default:
	}
}
