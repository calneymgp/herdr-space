package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"herdr-space/internal/agents"
)

// These are local phase costs on a controlled two-process ProcRoot. RPC phase
// timings and whole-scan latency are recorded by TestScanPhaseBaseline.
func BenchmarkInventoryLocalPhases(b *testing.B) {
	root := b.TempDir()
	uid := os.Getuid()
	fakeProc(b, root, 100, 1, 100, uid, "bash")
	fakeProc(b, root, 101, 100, 101, uid, "codex")
	m, err := New(Config{HerdrConfigDir: b.TempDir(), ProcRoot: root})
	if err != nil {
		b.Fatal(err)
	}
	if err := m.SetReferenceStore(context.Background(), &memoryRefs{}); err != nil {
		b.Fatal(err)
	}
	b.Run("DetectInPane", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = agents.DetectInPane(root, uid, 100, 101, []int{101})
		}
	})
	b.Run("List", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := agents.List(root, uid); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("PersistEmpty", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := m.persist(context.Background()); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// TestInventoryPhaseMetrics is an opt-in local fixture measurement. Durations
// include client transport; RPC phases are sequential and distinct here.
func TestInventoryPhaseMetrics(t *testing.T) {
	if os.Getenv("HERDR_PERF_RUN") != "1" {
		t.Skip("opt-in performance measurement")
	}
	for _, sc := range []struct {
		panes   int
		delay   time.Duration
		samples int
	}{{0, 0, 30}, {3, 5 * time.Millisecond, 12}} {
		t.Run(fmt.Sprintf("panes_%d_delay_%dms", sc.panes, sc.delay.Milliseconds()), func(t *testing.T) {
			dir := t.TempDir()
			f := &perfSocket{panes: sc.panes, delay: sc.delay}
			f.serve(t, filepath.Join(dir, "herdr.sock"))
			f.serve(t, filepath.Join(dir, "sessions", "two", "herdr.sock"))
			proc := t.TempDir()
			if sc.panes > 0 {
				fakeProc(t, proc, 100, 1, 100, os.Getuid(), "bash")
				fakeProc(t, proc, 101, 100, 101, os.Getuid(), "codex")
			}
			for i := -1; i < sc.samples; i++ {
				startup := time.Now()
				m, err := New(Config{HerdrConfigDir: dir, ProcRoot: proc, ScanInterval: time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				startupNs := time.Since(startup).Nanoseconds()
				var captured []scanPhases
				m.phaseObserver = func(p scanPhases) { captured = append(captured, p) }
				inv, err := m.Inventory(context.Background())
				if err != nil || inv.Stale || len(inv.Items) != 2*sc.panes {
					t.Fatalf("fixture inventory count=%d stale=%t err=%v", len(inv.Items), inv.Stale, err)
				}
				_, err = m.Inventory(context.Background())
				if err != nil || len(captured) != 2 || !captured[1].Cached {
					t.Fatalf("cache phase invalid: %v %d", err, len(captured))
				}
				if captured[0].ProcSnapshots != 1 || captured[1].ProcSnapshots != 0 {
					t.Fatalf("proc snapshots: scan=%d cache=%d", captured[0].ProcSnapshots, captured[1].ProcSnapshots)
				}
				if i >= 0 {
					p := captured[0]
					c := captured[1]
					t.Logf("PHASE panes=%d delay_ms=%d sample=%d startup_ns=%d total_ns=%d gate_wait_ns=%d snapshot_rpc_ns=%d process_rpc_ns=%d pane_rpc_ns=%d proc_snapshot_ns=%d classification_ns=%d publication_ns=%d cache_ns=%d snapshot_calls=%d process_calls=%d pane_calls=%d proc_snapshots=%d", sc.panes, sc.delay.Milliseconds(), i, startupNs, p.Total.Nanoseconds(), p.GateWait.Nanoseconds(), p.SnapshotRPC.Nanoseconds(), p.ProcessRPC.Nanoseconds(), p.PaneRPC.Nanoseconds(), p.ProcSnapshot.Nanoseconds(), p.Classification.Nanoseconds(), p.Publication.Nanoseconds(), c.Total.Nanoseconds(), p.SnapshotCalls, p.ProcessCalls, p.PaneCalls, p.ProcSnapshots)
				}
			}
		})
	}
}
