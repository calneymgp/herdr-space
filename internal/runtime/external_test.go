package runtime

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func fakeProc(t testing.TB, root string, pid, ppid, group, uid int, name string) {
	t.Helper()
	d := filepath.Join(root, strconv.Itoa(pid))
	if e := os.Mkdir(d, 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(d, "status"), []byte("Uid:\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\n"), 0600)
	os.WriteFile(filepath.Join(d, "stat"), []byte(strconv.Itoa(pid)+" ("+name+") S "+strconv.Itoa(ppid)+" "+strconv.Itoa(group)+" 10 5 0 0 0 0 0 0 0 0 0 0 0 0 0 0 777 0 0\n"), 0600)
	os.WriteFile(filepath.Join(d, "cmdline"), []byte(name+"\x00"), 0600)
	if name == "codex" || name == "claude" {
		os.Symlink("/usr/local/bin/"+name, filepath.Join(d, "exe"))
	}
}
func TestInventorySeparatesHERDRManagedAndStandaloneExternal(t *testing.T) {
	cfg := t.TempDir()
	proc := t.TempDir()
	uid := os.Getuid()
	fakeProc(t, proc, 100, 1, 100, uid, "bash")
	fakeProc(t, proc, 101, 100, 101, uid, "codex")
	foreignUID := 0
	if uid == 0 {
		foreignUID = 1
	}
	fakeProc(t, proc, 300, 1, 300, foreignUID, "sshd")
	fakeProc(t, proc, 201, 300, 201, uid, "claude")
	sock := filepath.Join(cfg, "herdr.sock")
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
					c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[{"pane_id":"p","terminal_id":"t","agent":"codex","agent_status":"working"}]}}}` + "\n"))
				case "pane.process_info":
					c.Write([]byte(`{"id":"1","result":{"process_info":{"pane_id":"p","shell_pid":100,"foreground_process_group_id":101,"foreground_processes":[{"pid":101}]}}}` + "\n"))
				case "pane.get":
					c.Write([]byte(`{"id":"1","result":{"pane":{}}}` + "\n"))
				}
			}()
		}
	}()
	m, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc})
	got, e := m.Inventory(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	managed, external := 0, 0
	for _, s := range got.Items {
		if s.Membership == "managed" {
			managed++
			if s.PID != 101 {
				t.Fatal("HERDR pane mismatched")
			}
		}
		if s.Membership == "external" {
			external++
			if s.PID != 201 || s.TerminalID != "" {
				t.Fatal("external duplicate or terminal claim")
			}
		}
	}
	if managed != 1 || external != 1 {
		t.Fatalf("memberships managed=%d external=%d", managed, external)
	}
}
