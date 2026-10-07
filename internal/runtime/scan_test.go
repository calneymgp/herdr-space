package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"herdr-space/internal/model"
)

type scanFixture struct {
	mu          sync.Mutex
	calls       map[string]int
	phases      map[string]time.Duration
	entered     chan struct{}
	block       <-chan struct{}
	delay       time.Duration
	panes       int
	getFail     bool
	watchActive int
}

func (f *scanFixture) serve(t *testing.T, socket string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req struct {
					Method string `json:"method"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				started := time.Now()
				f.mu.Lock()
				f.calls[req.Method]++
				if req.Method == "events.subscribe" {
					f.watchActive++
				}
				f.mu.Unlock()
				if req.Method == "events.subscribe" {
					defer func() { f.mu.Lock(); f.watchActive--; f.mu.Unlock() }()
				}
				if req.Method == "session.snapshot" && f.entered != nil {
					select {
					case f.entered <- struct{}{}:
					default:
					}
				}
				if f.block != nil {
					<-f.block
				}
				if f.delay > 0 {
					time.Sleep(f.delay)
				}
				var result any
				switch req.Method {
				case "session.snapshot":
					panes := make([]map[string]string, f.panes)
					for i := range panes {
						panes[i] = map[string]string{"pane_id": string(rune('a' + i)), "terminal_id": string(rune('A' + i)), "agent": "codex"}
					}
					result = map[string]any{"snapshot": map[string]any{"panes": panes}}
				case "pane.process_info":
					result = map[string]any{"process_info": map[string]any{"foreground_processes": []any{}}}
				case "pane.get":
					if f.getFail {
						return
					}
					result = map[string]any{"pane": map[string]any{}}
				case "events.subscribe":
					result = map[string]any{"subscribed": true}
				default:
					return
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": "1", "result": result})
				f.mu.Lock()
				if f.phases == nil {
					f.phases = map[string]time.Duration{}
				}
				f.phases[req.Method] += time.Since(started)
				f.mu.Unlock()
				if req.Method == "events.subscribe" {
					_, _ = io.Copy(io.Discard, conn)
				}
			}()
		}
	}()
}

func (f *scanFixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}
func (f *scanFixture) activeWatchers() int { f.mu.Lock(); defer f.mu.Unlock(); return f.watchActive }
func (f *scanFixture) phase(method string) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phases[method]
}

func TestInventoryDeadlineWhileScanBusy(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	f := &scanFixture{calls: map[string]int{}, entered: make(chan struct{}, 1), block: release, panes: 2}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	phases := make(chan scanPhases, 2)
	m.phaseObserver = func(p scanPhases) { phases <- p }
	firstDone := make(chan error, 1)
	go func() { _, err := m.Inventory(context.Background()); firstDone <- err }()
	select {
	case <-f.entered:
	case <-time.After(time.Second):
		t.Fatal("first scan did not enter snapshot")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = m.Inventory(ctx)
	elapsed := time.Since(start)
	close(release)
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatal(firstErr)
	}
	t.Logf("busy reader elapsed=%s, snapshots=%d", elapsed, f.count("session.snapshot"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want reader deadline, got %v", err)
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("reader waited %s after deadline", elapsed)
	}
	if got := f.count("session.snapshot"); got != 1 {
		t.Fatalf("queued reader started another scan: snapshots=%d", got)
	}
	first, second := <-phases, <-phases
	if first.GateWait < 20*time.Millisecond && second.GateWait < 20*time.Millisecond {
		t.Fatalf("scan gate wait missing: %s, %s", first.GateWait, second.GateWait)
	}
}

func TestScanPhaseBaseline(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, delay: 12 * time.Millisecond, panes: 3}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	f.serve(t, filepath.Join(dir, "sessions", "two", "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	inv, err := m.Inventory(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Stale || len(inv.Items) != 6 {
		t.Fatalf("inventory: %+v", inv)
	}
	t.Logf("scan total=%s snapshot=%d/%s process_info=%d/%s pane.get=%d/%s local+transport≈%s fake RPC delay=%s", elapsed, f.count("session.snapshot"), f.phase("session.snapshot"), f.count("pane.process_info"), f.phase("pane.process_info"), f.count("pane.get"), f.phase("pane.get"), elapsed-f.phase("session.snapshot")-f.phase("pane.process_info")-f.phase("pane.get"), f.delay)
}

func TestCanceledScanDoesNotPublishOrThrottleRetry(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, delay: 75 * time.Millisecond, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := m.Inventory(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled scan returned %v", err)
	}
	if !m.lastScan.IsZero() || len(m.inventory.Items) != 0 {
		t.Fatalf("canceled scan published state or throttled retry: %+v", m.inventory)
	}
	inv, err := m.Inventory(context.Background())
	if err != nil || inv.Stale || len(inv.Items) != 1 {
		t.Fatalf("retry inventory=%+v err=%v", inv, err)
	}
	if got := f.count("session.snapshot"); got != 2 {
		t.Fatalf("retry snapshots=%d", got)
	}
}

func TestPaneDetailFailureMarksInventoryIncomplete(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 2, getFail: true}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := m.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Stale || len(inv.Items) != 2 {
		t.Fatalf("incomplete inventory=%+v", inv)
	}
	for _, item := range inv.Items {
		if len(item.Capabilities) != 0 {
			t.Fatalf("incomplete pane has capabilities: %+v", item)
		}
	}
}

func TestStartCloseAllowsRestartWithoutWatcherLeak(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 1; cycle <= 3; cycle++ {
		m.Start(context.Background())
		deadline := time.After(time.Second)
		for f.count("events.subscribe") < cycle {
			select {
			case <-deadline:
				t.Fatalf("watcher did not start in cycle %d", cycle)
			default:
				time.Sleep(time.Millisecond)
			}
		}
		closed := make(chan struct{})
		go func() { _ = m.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatalf("Close blocked in cycle %d", cycle)
		}
		deadline = time.After(time.Second)
		for f.activeWatchers() != 0 {
			select {
			case <-deadline:
				t.Fatalf("watcher leaked in cycle %d", cycle)
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	if got := f.count("events.subscribe"); got != 3 {
		t.Fatalf("subscriptions=%d", got)
	}
}

func TestConcurrentCloseAndRestart(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	for cycle := 0; cycle < 20; cycle++ {
		deadline := time.After(time.Second)
		for f.count("events.subscribe") < cycle+1 {
			select {
			case <-deadline:
				t.Fatal("watcher failed to start")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		closed := make(chan struct{}, 2)
		go func() { _ = m.Close(); closed <- struct{}{} }()
		go func() { _ = m.Close(); closed <- struct{}{} }()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("first Close blocked")
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("second Close blocked")
		}
		deadline = time.After(time.Second)
		for f.activeWatchers() != 0 {
			select {
			case <-deadline:
				t.Fatal("watcher active after concurrent Close")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		m.Start(context.Background())
		deadline = time.After(time.Second)
		for f.count("events.subscribe") < cycle+2 {
			select {
			case <-deadline:
				t.Fatal("restart watcher failed to start")
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for f.activeWatchers() != 0 {
		select {
		case <-deadline:
			t.Fatal("watcher remained active after final Close")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

type blockingReferenceStore struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

type cancelingReferenceStore struct{ entered chan struct{} }

func (s *cancelingReferenceStore) LoadRuntimeReferences(context.Context) ([]model.RuntimeReference, error) {
	return nil, nil
}
func (s *cancelingReferenceStore) SaveRuntimeReferences(ctx context.Context, _ []model.RuntimeReference) error {
	close(s.entered)
	<-ctx.Done()
	return ctx.Err()
}

func TestInventoryDeadlineDuringReferenceSave(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store := &cancelingReferenceStore{entered: make(chan struct{})}
	if err := m.SetReferenceStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = m.Inventory(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Inventory returned %v after %s", err, elapsed)
	}
	if elapsed > 120*time.Millisecond {
		t.Fatalf("Inventory waited %s after save deadline", elapsed)
	}
	select {
	case <-store.entered:
	default:
		t.Fatal("save was not reached")
	}
	t.Logf("in-flight save reader elapsed=%s", elapsed)
}

func (s *blockingReferenceStore) LoadRuntimeReferences(context.Context) ([]model.RuntimeReference, error) {
	return nil, nil
}
func (s *blockingReferenceStore) SaveRuntimeReferences(ctx context.Context, _ []model.RuntimeReference) error {
	s.mu.Lock()
	s.calls++
	first := s.calls == 1
	s.mu.Unlock()
	if first {
		close(s.entered)
		<-s.release
	}
	return ctx.Err()
}

func TestInventoryDeadlineWhileReferenceSaveOwnsLock(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	store := &blockingReferenceStore{entered: make(chan struct{}), release: make(chan struct{})}
	if err := m.SetReferenceStore(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- m.persist(context.Background()) }()
	select {
	case <-store.entered:
	case <-time.After(time.Second):
		t.Fatal("first save did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	start := time.Now()
	go func() { _, err := m.Inventory(ctx); result <- err }()
	var got error
	select {
	case got = <-result:
	case <-time.After(120 * time.Millisecond):
		close(store.release)
		<-result
		t.Fatal("Inventory waited past its context for persist lock")
	}
	elapsed := time.Since(start)
	close(store.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatalf("Inventory returned %v after %s", got, elapsed)
	}
	t.Logf("persist contention reader elapsed=%s", elapsed)
}

func TestStartDuringCloseWaitsThenRestarts(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	deadline := time.After(time.Second)
	for f.count("events.subscribe") < 1 {
		select {
		case <-deadline:
			t.Fatal("watcher did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// Hold one in-flight watcher at the same WaitGroup barrier used by Close.
	m.watcherWG.Add(1)
	released := false
	defer func() {
		if !released {
			m.watcherWG.Done()
		}
	}()
	closed := make(chan struct{})
	go func() { _ = m.Close(); close(closed) }()
	deadline = time.After(time.Second)
	for {
		m.mu.RLock()
		closing := m.closeDone != nil
		m.mu.RUnlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			t.Fatal("Close did not begin")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	shortCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	shortDone := make(chan struct{})
	go func() { m.Start(shortCtx); close(shortDone) }()
	select {
	case <-shortDone:
		t.Fatal("Start returned before its context while Close was active")
	case <-time.After(5 * time.Millisecond):
	}
	select {
	case <-shortDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Start ignored its context during Close")
	}
	started := make(chan struct{})
	go func() { m.Start(context.Background()); close(started) }()
	select {
	case <-started:
		t.Fatal("Start returned while Close was still waiting")
	case <-time.After(25 * time.Millisecond):
	}
	m.watcherWG.Done()
	released = true
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Start did not retry after Close")
	}
	deadline = time.After(time.Second)
	for f.count("events.subscribe") < 2 {
		select {
		case <-deadline:
			t.Fatal("restart watcher did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProcSnapshotFailureMakesInventoryStale(t *testing.T) {
	dir := t.TempDir()
	f := &scanFixture{calls: map[string]int{}, panes: 1}
	f.serve(t, filepath.Join(dir, "herdr.sock"))
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := m.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !inv.Stale || len(inv.Items) != 1 || len(inv.Items[0].Capabilities) != 0 {
		t.Fatalf("partial proc inventory granted capabilities: stale=%t items=%d", inv.Stale, len(inv.Items))
	}
}
