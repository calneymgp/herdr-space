package agents

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDetectWrapperChainOnce(t *testing.T) {
	root := t.TempDir()
	for _, p := range []struct {
		pid, ppid int
		cmd       string
	}{{10, 1, "opencode"}, {11, 10, "opencode2"}, {12, 11, "opencode2-wrapped"}} {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		os.Mkdir(dir, 0700)
		os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(strconv.Itoa(p.pid)+" ("+p.cmd+") S "+strconv.Itoa(p.ppid)+" 10 10 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 12345 0 0\n"), 0600)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p.cmd+"\x00"), 0600)
		os.Symlink("/usr/local/bin/"+p.cmd, filepath.Join(dir, "exe"))
	}
	got := Detect(root, 1000, []int{10, 11, 12})
	if len(got) != 1 || got[0].Agent != "opencode" || got[0].PID != 12 {
		t.Fatalf("unexpected detection: %+v", got)
	}
}
func TestDetectPiThroughNodePackagePath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "41")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
	os.WriteFile(filepath.Join(dir, "stat"), []byte("41 (node) S 1 41 41 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 321 0 0\n"), 0600)
	script := filepath.Join(t.TempDir(), "node_modules", "@earendil-works", "pi-coding-agent", "dist", "cli.js")
	os.MkdirAll(filepath.Dir(script), 0700)
	os.WriteFile(script, []byte(""), 0600)
	os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/home/u/.nvm/versions/node/v24.20.0/bin/node\x00"+script+"\x00"), 0600)
	os.Symlink("/usr/bin/node", filepath.Join(dir, "exe"))
	got := Detect(root, 1000, []int{41})
	if len(got) != 1 || got[0].Agent != "pi" {
		t.Fatalf("pi not recognized: %+v", got)
	}
}
func TestDetectInPaneFindsDescendantInForegroundGroup(t *testing.T) {
	root := t.TempDir()
	for _, p := range []struct {
		pid, ppid, group int
		cmd              string
	}{{10, 1, 10, "bash"}, {12, 10, 12, "sh"}, {14, 12, 12, "codex"}} {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		os.Mkdir(dir, 0700)
		os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(strconv.Itoa(p.pid)+" ("+p.cmd+") S "+strconv.Itoa(p.ppid)+" "+strconv.Itoa(p.group)+" 10 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 99 0 0\n"), 0600)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p.cmd+"\x00"), 0600)
		if p.cmd == "codex" {
			os.Symlink("/usr/local/bin/codex", filepath.Join(dir, "exe"))
		}
	}
	got := DetectInPane(root, 1000, 10, 12, []int{12})
	if len(got) != 1 || got[0].PID != 14 || got[0].Agent != "codex" {
		t.Fatalf("descendant missed: %+v", got)
	}
}
func TestListExcludesHeadlessAgentHelpers(t *testing.T) {
	root := t.TempDir()
	for _, p := range []struct {
		pid, tty int
		args     string
	}{{11, 0, "codex\x00app-server\x00"}, {12, 5, "codex\x00mcp-server\x00"}, {13, 5, "codex\x00"}} {
		dir := filepath.Join(root, strconv.Itoa(p.pid))
		os.Mkdir(dir, 0700)
		os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(strconv.Itoa(p.pid)+" (codex) S 1 "+strconv.Itoa(p.pid)+" "+strconv.Itoa(p.pid)+" "+strconv.Itoa(p.tty)+" 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100 0 0\n"), 0600)
		os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p.args), 0600)
		os.Symlink("/usr/local/bin/codex", filepath.Join(dir, "exe"))
	}
	got, e := List(root, 1000)
	if e != nil || len(got) != 1 || got[0].PID != 13 {
		t.Fatalf("helpers listed: %+v %v", got, e)
	}
}
func TestListRejectsForgedArgvZeroWithGenericExecutable(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "51")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
	os.WriteFile(filepath.Join(dir, "stat"), []byte("51 (sleep) S 1 51 51 5 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100 0 0\n"), 0600)
	os.WriteFile(filepath.Join(dir, "cmdline"), []byte("codex\x00"), 0600)
	os.Symlink("/usr/bin/sleep", filepath.Join(dir, "exe"))
	got, e := List(root, 1000)
	if e != nil || len(got) != 0 {
		t.Fatalf("forged argv accepted: %+v %v", got, e)
	}
}

func TestDetectInPaneRejectsForgedAgentArgvWithGenericExecutable(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "71")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte("71 (bash) S 70 71 71 5 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100 0 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("codex\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/bin/bash", filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	if got := DetectInPane(root, 1000, 70, 71, []int{71}); len(got) != 0 {
		t.Fatalf("forged managed process accepted: %+v", got)
	}
}
func TestListRejectsNodeDataMentioningAgentPackage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "61")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "status"), []byte("Uid:\t1000\t1000\t1000\t1000\n"), 0600)
	os.WriteFile(filepath.Join(dir, "stat"), []byte("61 (node) S 1 61 61 5 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100 0 0\n"), 0600)
	os.WriteFile(filepath.Join(dir, "cmdline"), []byte("node\x00-e\x00console.log('@earendil-works/pi-coding-agent/dist/cli.js')\x00"), 0600)
	os.Symlink("/usr/bin/node", filepath.Join(dir, "exe"))
	got, e := List(root, 1000)
	if e != nil || len(got) != 0 {
		t.Fatalf("node data accepted: %+v %v", got, e)
	}
}
