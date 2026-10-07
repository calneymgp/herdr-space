package agents

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func fixtureProcess(t *testing.T, root string, pid, ppid, group, uid, tty int, name, start string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	stat := strconv.Itoa(pid) + " (" + name + ") S " + strconv.Itoa(ppid) + " " + strconv.Itoa(group) + " 10 " + strconv.Itoa(tty) + " 0 0 0 0 0 0 0 0 0 0 0 0 0 0 " + start + " 0 0\n"
	for file, value := range map[string]string{"stat": stat, "status": "Uid:\t" + strconv.Itoa(uid) + "\n", "cmdline": name + "\x00"} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/usr/local/bin/"+name, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotSharesEnumerationAndKeepsClassificationFresh(t *testing.T) {
	root := t.TempDir()
	fixtureProcess(t, root, 100, 1, 100, 1000, 5, "bash", "10")
	fixtureProcess(t, root, 101, 100, 101, 1000, 5, "sh", "11")
	fixtureProcess(t, root, 102, 101, 101, 1000, 5, "codex", "12")
	fixtureProcess(t, root, 201, 1, 201, 1000, 5, "claude", "21")
	fixtureProcess(t, root, 301, 1, 301, 2000, 5, "codex", "31")
	reads := 0
	snap, err := newProcSnapshot(root, 1000, func(path string) ([]os.DirEntry, error) { reads++; return os.ReadDir(path) })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		got := snap.DetectInPane(100, 101, []int{101})
		if len(got) != 1 || got[0].PID != 102 || got[0].StartTime != "12" {
			t.Fatalf("pane %d: %+v", i, got)
		}
	}
	global := snap.List()
	if len(global) != 2 {
		t.Fatalf("global: %+v", global)
	}
	if reads != 1 {
		t.Fatalf("proc enumerations=%d want 1", reads)
	}
	if err := os.RemoveAll(filepath.Join(root, "101")); err != nil {
		t.Fatal(err)
	}
	fixtureProcess(t, root, 101, 1, 101, 1000, 5, "sh", "99")
	if got := snap.DetectInPane(100, 101, nil); len(got) != 0 {
		t.Fatalf("reused ancestor inherited pane: %+v", got)
	}
	// A reused descendant PID must not inherit the old ancestry or identity.
	if err := os.RemoveAll(filepath.Join(root, "102")); err != nil {
		t.Fatal(err)
	}
	fixtureProcess(t, root, 102, 1, 101, 1000, 5, "codex", "99")
	if got := snap.DetectInPane(100, 101, nil); len(got) != 0 {
		t.Fatalf("reused PID inherited pane: %+v", got)
	}
	// Explicit foreground is classified from the current process, even if absent at snapshot time.
	fixtureProcess(t, root, 103, 100, 103, 1000, 5, "codex", "13")
	if got := snap.DetectInPane(100, 103, []int{103}); len(got) != 1 || got[0].PID != 103 {
		t.Fatalf("new explicit foreground: %+v", got)
	}
}

func TestSnapshotPreservesPaneAndInventoryLimits(t *testing.T) {
	root := t.TempDir()
	fixtureProcess(t, root, 100, 1, 100, 1000, 5, "bash", "10")
	fixtureProcess(t, root, 101, 100, 101, 1000, 5, "codex", "11")
	normal, err := Snapshot(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := normal.DetectInPane(100, 101, nil); len(got) != 1 {
		t.Fatalf("descendant missing: %+v", got)
	}
	normal.entries = 4097
	if got := normal.DetectInPane(100, 101, nil); len(got) != 0 {
		t.Fatalf("pane over limit traversed: %+v", got)
	}
	if got := normal.DetectInPane(100, 101, []int{101}); len(got) != 1 {
		t.Fatalf("explicit foreground lost: %+v", got)
	}
	_, err = newProcSnapshot(root, 1000, func(string) ([]os.DirEntry, error) { return make([]os.DirEntry, 65537), nil })
	if err == nil {
		t.Fatal("oversized inventory accepted")
	}
	_, err = Snapshot(filepath.Join(root, "missing"), 1000)
	if err == nil {
		t.Fatal("unreadable proc accepted")
	}
}

func TestSnapshotUncertainAncestryExcludesExternal(t *testing.T) {
	root := t.TempDir()
	fixtureProcess(t, root, 100, 1, 100, 1000, 5, "bash", "10")
	fixtureProcess(t, root, 101, 1, 101, 1000, 5, "sh", "11")
	fixtureProcess(t, root, 102, 101, 101, 1000, 5, "codex", "12")
	snap, err := Snapshot(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "101")); err != nil {
		t.Fatal(err)
	}
	fixtureProcess(t, root, 101, 100, 101, 1000, 5, "sh", "99")
	if !snap.IsDescendant(102, 100) {
		t.Fatal("uncertain ancestry allowed external control")
	}
}

func TestSnapshotKeepsReadableForeignUIDAncestorForExternal(t *testing.T) {
	root := t.TempDir()
	fixtureProcess(t, root, 100, 1, 100, 1000, 5, "bash", "10")
	fixtureProcess(t, root, 300, 1, 300, 0, 5, "sshd", "30")
	fixtureProcess(t, root, 201, 300, 201, 1000, 5, "codex", "21")
	fixtureProcess(t, root, 301, 100, 101, 0, 5, "codex", "31")
	if got := IsDescendant(root, 201, 100); got {
		t.Fatal("legacy ancestry incorrectly marked external agent inside pane")
	}
	legacy, err := List(root, 1000)
	if err != nil || len(legacy) != 1 || legacy[0].PID != 201 {
		t.Fatalf("legacy list lost external: count=%d err=%v", len(legacy), err)
	}
	snap, err := Snapshot(root, 1000)
	if err != nil {
		t.Fatal(err)
	}
	global := snap.List()
	if len(global) != 1 || global[0].PID != 201 {
		t.Fatalf("snapshot list lost external: count=%d", len(global))
	}
	if snap.IsDescendant(201, 100) {
		t.Fatal("readable foreign-UID ancestor incorrectly excluded external")
	}
	if got := snap.DetectInPane(100, 101, nil); len(got) != 0 {
		t.Fatalf("foreign-UID candidate inherited managed pane: count=%d", len(got))
	}
	if err := os.RemoveAll(filepath.Join(root, "300")); err != nil {
		t.Fatal(err)
	}
	fixtureProcess(t, root, 300, 1, 300, 0, 5, "sshd", "99")
	if !snap.IsDescendant(201, 100) {
		t.Fatal("reused foreign-UID ancestor allowed external control")
	}
}
