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
)

func TestManagedStopRejectsReplacedPaneProcess(t *testing.T) {
	cfg := t.TempDir()
	proc := t.TempDir()
	uid := os.Getuid()
	fakeProc(t, proc, 100, 1, 100, uid, "bash")
	fakeProc(t, proc, 101, 100, 101, uid, "codex")
	fakeProc(t, proc, 202, 100, 202, uid, "codex")
	sock := filepath.Join(cfg, "herdr.sock")
	ln, e := net.Listen("unix", sock)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	var infoCalls, closed atomic.Int32
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
					c.Write([]byte(`{"id":"1","result":{"snapshot":{"panes":[{"pane_id":"p","terminal_id":"t","agent":"codex"}]}}}` + "\n"))
				case "pane.process_info":
					pid := 101
					if infoCalls.Add(1) > 1 {
						pid = 202
					}
					c.Write([]byte(`{"id":"1","result":{"process_info":{"pane_id":"p","shell_pid":100,"foreground_process_group_id":101,"foreground_processes":[{"pid":` + json.Number(fmt.Sprint(pid)).String() + `}]}}}` + "\n"))
				case "pane.get":
					c.Write([]byte(`{"id":"1","result":{"pane":{"terminal_id":"t"}}}` + "\n"))
				case "pane.close":
					closed.Add(1)
					c.Write([]byte(`{"id":"1","result":{"type":"pane_close"}}` + "\n"))
				}
			}()
		}
	}()
	m, _ := New(Config{HerdrConfigDir: cfg, ProcRoot: proc})
	id := socketID(sock) + ":t"
	if e := m.Stop(context.Background(), id, false); e == nil {
		t.Fatal("replacement accepted")
	}
	if closed.Load() != 0 {
		t.Fatal("replacement pane closed")
	}
}
