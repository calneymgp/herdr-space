package runtime

import (
	"context"
	"encoding/json"
	"herdr-space/internal/model"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInventoryUsesStableTerminalIdentity(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
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
					c.Write([]byte(`{"id":"1","result":{"snapshot":{"version":"0.9.1","panes":[{"pane_id":"w1:p1","terminal_id":"term_1","workspace_id":"w1","cwd":"/tmp","agent":"codex","agent_status":"working"}]}}}` + "\n"))
				case "pane.process_info":
					c.Write([]byte(`{"id":"1","result":{"process_info":{"pane_id":"w1:p1","shell_pid":0,"foreground_processes":[]}}}` + "\n"))
				case "pane.get":
					c.Write([]byte(`{"id":"1","result":{"pane":{"agent_session":null}}}` + "\n"))
				}
			}()
		}
	}()
	m, e := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	got, e := m.Inventory(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Items) != 1 || got.Items[0].TerminalID != "term_1" || got.Items[0].ID == "" {
		t.Fatalf("inventory shape: %+v", got)
	}
	if got.Items[0].Alive {
		t.Fatal("unverified process marked alive")
	}
	if _, err := m.Attach(context.Background(), model.StreamRequest{ID: got.Items[0].ID, Mode: "control", Cols: 80, Rows: 24}); err == nil {
		t.Fatal("control allowed without verified capability")
	}
	m.Close()
}
func TestUnavailableMarksInventoryStale(t *testing.T) {
	dir := t.TempDir()
	m, e := New(Config{HerdrConfigDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	got, _ := m.Inventory(context.Background())
	if !got.Stale {
		t.Fatal("missing socket must be stale")
	}
	os.MkdirAll(filepath.Join(dir, "sessions", "work"), 0700)
	m.Close()
}

func TestInventoryScopesSpacesAndUsesTabNames(t *testing.T) {
	dir := t.TempDir()
	serve := func(sock, label, path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(sock), 0700); err != nil {
			t.Fatal(err)
		}
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				go func() {
					defer c.Close()
					var req struct {
						Method string `json:"method"`
					}
					_ = json.NewDecoder(c).Decode(&req)
					switch req.Method {
					case "session.snapshot":
						_ = json.NewEncoder(c).Encode(map[string]any{"id": "1", "result": map[string]any{"snapshot": map[string]any{"workspaces": []any{map[string]any{"workspace_id": "same", "label": label}, map[string]any{"workspace_id": "empty", "label": "Empty " + label}}, "tabs": []any{map[string]any{"tab_id": "same-tab", "workspace_id": "same", "label": "Tab " + label}}, "panes": []any{map[string]any{"pane_id": "p-" + label, "terminal_id": "term-" + label, "workspace_id": "same", "tab_id": "same-tab", "cwd": path, "foreground_cwd": "/foreground", "terminal_title_stripped": "Terminal title", "agent": "codex"}}}}})
					case "pane.process_info":
						_, _ = c.Write([]byte("{\"id\":\"1\",\"result\":{\"process_info\":{\"foreground_processes\":[]}}}\n"))
					case "pane.get":
						_, _ = c.Write([]byte("{\"id\":\"1\",\"result\":{\"pane\":{}}}\n"))
					}
				}()
			}
		}()
	}
	serve(filepath.Join(dir, "herdr.sock"), "One", "/one")
	serve(filepath.Join(dir, "sessions", "two", "herdr.sock"), "Two", "/two")
	m, err := New(Config{HerdrConfigDir: dir, ProcRoot: t.TempDir(), ScanInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := m.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Spaces) != 4 || len(inv.Items) != 2 {
		t.Fatalf("spaces=%d sessions=%d", len(inv.Spaces), len(inv.Items))
	}
	seen := map[string]bool{}
	for _, space := range inv.Spaces {
		if seen[space.ID] {
			t.Fatalf("duplicate scoped space ID: %q", space.ID)
		}
		seen[space.ID] = true
		if space.WorkspaceID == "empty" && space.Path != "" {
			t.Fatalf("empty workspace got path %q", space.Path)
		}
		if space.WorkspaceID == "same" && space.Path != "/one" && space.Path != "/two" {
			t.Fatalf("wrong workspace path: %q", space.Path)
		}
	}
	for _, session := range inv.Items {
		if session.Name != "Tab One" && session.Name != "Tab Two" {
			t.Fatalf("tab name lost: %q", session.Name)
		}
	}
}
