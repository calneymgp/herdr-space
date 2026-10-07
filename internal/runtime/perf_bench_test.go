package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type perfSocket struct {
	calls         atomic.Int64
	snapshotCalls atomic.Int64
	processCalls  atomic.Int64
	paneCalls     atomic.Int64
	snapshotNs    atomic.Int64
	processNs     atomic.Int64
	paneNs        atomic.Int64
	delay         time.Duration
	panes         int
}

func (f *perfSocket) serve(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
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
				f.calls.Add(1)
				phaseStart := time.Now()
				if f.delay > 0 {
					time.Sleep(f.delay)
				}
				var result any
				switch req.Method {
				case "session.snapshot":
					panes := make([]map[string]string, f.panes)
					for i := range panes {
						panes[i] = map[string]string{"pane_id": fmt.Sprintf("p%d", i), "terminal_id": fmt.Sprintf("t%d", i), "agent": "codex"}
					}
					result = map[string]any{"snapshot": map[string]any{"panes": panes}}
				case "pane.process_info":
					result = map[string]any{"process_info": map[string]any{"shell_pid": 100, "foreground_process_group_id": 101, "foreground_processes": []any{map[string]int{"pid": 101}}}}
				case "pane.get":
					result = map[string]any{"pane": map[string]any{}}
				default:
					return
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": "1", "result": result})
				ns := time.Since(phaseStart).Nanoseconds()
				switch req.Method {
				case "session.snapshot":
					f.snapshotCalls.Add(1)
					f.snapshotNs.Add(ns)
				case "pane.process_info":
					f.processCalls.Add(1)
					f.processNs.Add(ns)
				case "pane.get":
					f.paneCalls.Add(1)
					f.paneNs.Add(ns)
				}
			}()
		}
	}()
}
func TestPerfInventory(t *testing.T) {
	if os.Getenv("HERDR_PERF_RUN") != "1" {
		t.Skip("opt-in performance measurement")
	}
	for _, sc := range []struct {
		sessions, panes, samples int
		delay                    time.Duration
	}{{1, 0, 100, 0}, {2, 3, 12, 5 * time.Millisecond}} {
		t.Run(fmt.Sprintf("sessions_%d_panes_%d_delay_%dms", sc.sessions, sc.panes, sc.delay.Milliseconds()), func(t *testing.T) {
			dir := t.TempDir()
			f := &perfSocket{delay: sc.delay, panes: sc.panes}
			for i := 0; i < sc.sessions; i++ {
				p := filepath.Join(dir, "sessions", fmt.Sprintf("%d", i), "herdr.sock")
				f.serve(t, p)
			}
			proc := t.TempDir()
			if sc.panes > 0 {
				fakeProc(t, proc, 100, 1, 100, os.Getuid(), "bash")
				fakeProc(t, proc, 101, 100, 101, os.Getuid(), "codex")
			}
			for i := -1; i < sc.samples; i++ {
				m, e := New(Config{HerdrConfigDir: dir, ProcRoot: proc, ScanInterval: time.Hour})
				if e != nil {
					t.Fatal(e)
				}
				before := f.calls.Load()
				bs, bp, bg := f.snapshotCalls.Load(), f.processCalls.Load(), f.paneCalls.Load()
				bsn, bpn, bgn := f.snapshotNs.Load(), f.processNs.Load(), f.paneNs.Load()
				start := time.Now()
				inv, e := m.Inventory(context.Background())
				elapsed := time.Since(start)
				if e != nil {
					t.Fatal(e)
				}
				if inv.Stale || len(inv.Items) != sc.sessions*sc.panes {
					t.Fatalf("stale=%t items=%d want=%d", inv.Stale, len(inv.Items), sc.sessions*sc.panes)
				}
				for _, item := range inv.Items {
					if !item.Alive || len(item.Capabilities) != 3 {
						t.Fatalf("managed fixture capability mismatch: alive=%t capabilities=%d", item.Alive, len(item.Capabilities))
					}
				}
				if i >= 0 {
					t.Logf("PERF kind=inventory sessions=%d panes_per_session=%d delay_ms=%d sample=%d elapsed_ns=%d rpc_calls=%d snapshot_calls=%d process_calls=%d pane_calls=%d snapshot_server_ns=%d process_server_ns=%d pane_server_ns=%d", sc.sessions, sc.panes, sc.delay.Milliseconds(), i, elapsed.Nanoseconds(), f.calls.Load()-before, f.snapshotCalls.Load()-bs, f.processCalls.Load()-bp, f.paneCalls.Load()-bg, f.snapshotNs.Load()-bsn, f.processNs.Load()-bpn, f.paneNs.Load()-bgn)
				}
			}
		})
	}
}
