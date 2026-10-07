package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"herdr-space/internal/model"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

type memoryRefs struct {
	mu    sync.Mutex
	items []model.RuntimeReference
	fail  atomic.Bool
}

func (s *memoryRefs) LoadRuntimeReferences(context.Context) ([]model.RuntimeReference, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.RuntimeReference(nil), s.items...), nil
}
func (s *memoryRefs) SaveRuntimeReferences(_ context.Context, v []model.RuntimeReference) error {
	if s.fail.Load() {
		return errors.New("store unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]model.RuntimeReference(nil), v...)
	return nil
}
func TestRestartShowsResumeForExitedProvenPane(t *testing.T) {
	cfg := t.TempDir()
	proc := t.TempDir()
	uid := os.Getuid()
	fakeProc(t, proc, 100, 1, 100, uid, "bash")
	fakeProc(t, proc, 101, 100, 101, uid, "codex")
	sock := filepath.Join(cfg, "herdr.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	var active atomic.Bool
	active.Store(true)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				var r struct {
					Method string `json:"method"`
				}
				json.NewDecoder(c).Decode(&r)
				switch r.Method {
				case "session.snapshot":
					if active.Load() {
						c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[{"pane_id":"p","terminal_id":"t","agent":"codex"}]}}}` + "\n"))
					} else {
						c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[]}}}` + "\n"))
					}
				case "pane.process_info":
					c.Write([]byte(`{"id":"1","result":{"process_info":{"pane_id":"p","shell_pid":100,"foreground_process_group_id":101,"foreground_processes":[{"pid":101}]}}}` + "\n"))
				case "pane.get":
					c.Write([]byte(`{"id":"1","result":{"pane":{"terminal_id":"t","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"proven-ref"}}}}` + "\n"))
				}
			}()
		}
	}()
	refs := &memoryRefs{}
	first, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc})
	if e := first.SetReferenceStore(context.Background(), refs); e != nil {
		t.Fatal(e)
	}
	got, _ := first.Inventory(context.Background())
	if len(got.Items) != 1 || !got.Items[0].Alive {
		t.Fatalf("first inventory: %+v", got)
	}
	stored, _ := refs.LoadRuntimeReferences(context.Background())
	if len(stored) != 1 {
		t.Fatalf("reference not saved: %d", len(stored))
	}
	active.Store(false)
	os.RemoveAll(filepath.Join(proc, "101"))
	second, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc})
	if e := second.SetReferenceStore(context.Background(), refs); e != nil {
		t.Fatal(e)
	}
	again, _ := second.Inventory(context.Background())
	if len(again.Items) != 1 || again.Items[0].Alive || !hasCapability(again.Items[0], "resume") {
		t.Fatalf("closed proven session missing: %+v", again)
	}
}
func TestExistingMalformedProcDoesNotProveExit(t *testing.T) {
	proc := t.TempDir()
	os.Mkdir(filepath.Join(proc, "77"), 0700)
	m, _ := New(Config{ProcRoot: proc, HerdrConfigDir: t.TempDir()})
	e := entry{session: model.Session{PID: 77, StartTime: "123"}}
	if exited(m, e) {
		t.Fatal("malformed existing proc treated as exited")
	}
}
func TestResumeConsumesReferenceBeforeStartingAgain(t *testing.T) {
	cfg := t.TempDir()
	proc := t.TempDir()
	project := t.TempDir()
	sock := filepath.Join(cfg, "herdr.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	var launches atomic.Int32
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				var r struct {
					Method string `json:"method"`
				}
				json.NewDecoder(c).Decode(&r)
				switch r.Method {
				case "session.snapshot":
					c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[]}}}` + "\n"))
				case "workspace.create":
					launches.Add(1)
					c.Write([]byte(`{"id":"1","result":{"workspace":{"workspace_id":"w2"},"root_pane":{"pane_id":"p2","terminal_id":"t2"}}}` + "\n"))
				case "agent.start":
					c.Write([]byte(`{"id":"1","result":{"type":"agent_started"}}` + "\n"))
				}
			}()
		}
	}()
	id := socketID(sock) + ":t1"
	id2 := socketID(sock) + ":t0"
	refs := &memoryRefs{items: []model.RuntimeReference{{Session: model.Session{ID: id, Source: "herdr", Membership: "managed", Agent: "codex", Launcher: "codex", TerminalID: "t1", PID: 77, StartTime: "123"}, Socket: sock, AgentRef: "proven-ref"}, {Session: model.Session{ID: id2, Source: "herdr", Membership: "managed", Agent: "codex", Launcher: "codex", TerminalID: "t0", PID: 78, StartTime: "124"}, Socket: sock, AgentRef: "proven-ref"}}}
	m, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc, ProjectRoots: []string{project}})
	if e := m.SetReferenceStore(context.Background(), refs); e != nil {
		t.Fatal(e)
	}
	r := model.StartRequest{ProjectPath: project}
	refs.fail.Store(true)
	if _, err := m.Resume(context.Background(), id, r); err == nil {
		t.Fatal("resume started without persisted reservation")
	}
	if launches.Load() != 0 {
		t.Fatal("launch happened before durable reservation")
	}
	refs.fail.Store(false)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := m.Resume(context.Background(), id, r); results <- err }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent resume successes %d", successes)
	}
	if _, e := m.Resume(context.Background(), id, r); e == nil {
		t.Fatal("same reference resumed twice")
	}
	if _, e := m.Resume(context.Background(), id2, r); e == nil {
		t.Fatal("same conversation resumed through second terminal")
	}
	if launches.Load() != 1 {
		t.Fatalf("launch count %d", launches.Load())
	}
	saved, _ := refs.LoadRuntimeReferences(context.Background())
	for _, x := range saved {
		if x.Session.ID == id || x.Session.ID == id2 {
			t.Fatal("consumed reference persisted")
		}
	}
}
func TestResumeRejectsLiveReplacementOrSameConversationElsewhere(t *testing.T) {
	for _, tc := range []struct{ name, terminal, ref string }{{"same terminal replaced", "t1", "new-ref"}, {"same conversation in other terminal", "t2", "proven-ref"}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := t.TempDir()
			proc := t.TempDir()
			project := t.TempDir()
			uid := os.Getuid()
			fakeProc(t, proc, 100, 1, 100, uid, "bash")
			fakeProc(t, proc, 101, 100, 101, uid, "codex")
			sock := filepath.Join(cfg, "herdr.sock")
			ln, e := net.Listen("unix", sock)
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			var launches atomic.Int32
			go func() {
				for {
					c, e := ln.Accept()
					if e != nil {
						return
					}
					go func() {
						defer c.Close()
						var r struct {
							Method string `json:"method"`
						}
						json.NewDecoder(c).Decode(&r)
						switch r.Method {
						case "session.snapshot":
							c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[{"pane_id":"p","terminal_id":"` + tc.terminal + `","agent":"codex"}]}}}` + "\n"))
						case "pane.process_info":
							c.Write([]byte(`{"id":"1","result":{"process_info":{"pane_id":"p","shell_pid":100,"foreground_process_group_id":101,"foreground_processes":[{"pid":101}]}}}` + "\n"))
						case "pane.get":
							c.Write([]byte(`{"id":"1","result":{"pane":{"terminal_id":"` + tc.terminal + `","agent_session":{"source":"herdr:codex","agent":"codex","kind":"id","value":"` + tc.ref + `"}}}}` + "\n"))
						case "workspace.create":
							launches.Add(1)
							c.Write([]byte(`{"id":"1","result":{}}` + "\n"))
						}
					}()
				}
			}()
			id := socketID(sock) + ":t1"
			refs := &memoryRefs{items: []model.RuntimeReference{{Session: model.Session{ID: id, Source: "herdr", Membership: "managed", Agent: "codex", Launcher: "codex", TerminalID: "t1", PID: 77, StartTime: "123"}, Socket: sock, AgentRef: "proven-ref"}}}
			m, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc, ProjectRoots: []string{project}})
			if e := m.SetReferenceStore(context.Background(), refs); e != nil {
				t.Fatal(e)
			}
			if _, e := m.Resume(context.Background(), id, model.StartRequest{ProjectPath: project}); e == nil {
				t.Fatal("live conversation accepted")
			}
			if launches.Load() != 0 {
				t.Fatal("workspace created for active/replaced conversation")
			}
		})
	}
}
