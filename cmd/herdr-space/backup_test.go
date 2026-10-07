package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"herdr-space/internal/auth"
	"herdr-space/internal/model"
	"herdr-space/internal/store"
)

func backupFixture(t *testing.T) (string, *store.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "space.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a, err := auth.New(s, filepath.Join(dir, "auth.key"))
	if err != nil {
		t.Fatal(err)
	}
	password := "a long disposable password"
	secret, _, err := a.Setup(context.Background(), "owner", password)
	if err != nil {
		t.Fatal(err)
	}
	return dir, s, password, secret
}

func visibleBundles(t *testing.T, dir string) []string {
	t.Helper()
	items, err := filepath.Glob(filepath.Join(dir, "backups", "space-*.bundle"))
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestBackupRestoresDatabaseAndAuthenticationKey(t *testing.T) {
	dir, s, password, secret := backupFixture(t)
	ctx := context.Background()
	project, err := s.CreateProject(ctx, model.Project{Name: "project", Path: "/tmp/project"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, model.Task{Title: "task", Description: "restored task", ProjectID: project.ID, Status: "doing", DueDate: "2026-10-05"})
	if err != nil {
		t.Fatal(err)
	}
	note, err := s.CreateNote(ctx, model.Note{Title: "note", Body: "# Restored note", ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPreferences(ctx, model.Preferences{Theme: "dark"}); err != nil {
		t.Fatal(err)
	}
	ref := model.RuntimeReference{Session: model.Session{ID: "restored-reference", Source: "herdr", Agent: "codex", Launcher: "codex"}, Socket: "/tmp/isolated-herdr.sock", AgentRef: "isolated-conversation"}
	if err := s.SaveRuntimeReferences(ctx, []model.RuntimeReference{ref}); err != nil {
		t.Fatal(err)
	}
	if _, err := runBackup(dir, 7); err != nil {
		t.Fatal(err)
	}
	bundles := visibleBundles(t, dir)
	if len(bundles) != 1 {
		t.Fatalf("bundles: %d", len(bundles))
	}
	entries, err := os.ReadDir(bundles[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("bundle has %d files", len(entries))
	}
	if info, err := os.Stat(bundles[0]); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("bundle directory mode: %v %v", info, err)
	}
	for _, name := range []string{"space.db", "auth.key"} {
		info, err := os.Stat(filepath.Join(bundles[0], name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode: %v %v", name, info, err)
		}
	}
	restored := t.TempDir()
	for _, name := range []string{"space.db", "auth.key"} {
		data, err := os.ReadFile(filepath.Join(bundles[0], name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(restored, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	copyDB, err := store.Open(filepath.Join(restored, "space.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	copyAuth, err := auth.New(copyDB, filepath.Join(restored, "auth.key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := copyAuth.Login(ctx, "owner", password, auth.Code(secret, time.Now()), time.Now()); err != nil {
		t.Fatalf("restored login: %v", err)
	}
	projects, err := copyDB.ListProjects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("restored projects: %d, %v", len(projects), err)
	}
	gotTask, err := copyDB.GetTask(ctx, task.ID)
	if err != nil || gotTask != task {
		t.Fatal("restored task differs")
	}
	gotNote, err := copyDB.GetNote(ctx, note.ID)
	if err != nil || gotNote != note {
		t.Fatal("restored note differs")
	}
	prefs, err := copyDB.Preferences(ctx)
	if err != nil || prefs.Theme != "dark" {
		t.Fatal("restored preferences differ")
	}
	refs, err := copyDB.LoadRuntimeReferences(ctx)
	if err != nil || len(refs) != 1 || refs[0].Session.ID != ref.Session.ID || refs[0].AgentRef != ref.AgentRef || refs[0].Socket != ref.Socket {
		t.Fatal("restored runtime reference differs")
	}
}

func TestBackupRenewsCurrentDayAndPreservesPriorOnBadKey(t *testing.T) {
	dir, s, _, _ := backupFixture(t)
	if _, err := runBackup(dir, 7); err != nil {
		t.Fatal(err)
	}
	first := visibleBundles(t, dir)
	if len(first) != 1 {
		t.Fatalf("first bundles: %d", len(first))
	}
	if _, err := s.CreateTask(context.Background(), model.Task{Title: "new task"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runBackup(dir, 7); err != nil {
		t.Fatal(err)
	}
	second := visibleBundles(t, dir)
	if len(second) != 1 || second[0] == first[0] {
		t.Fatalf("daily snapshot was not renewed: %v -> %v", first, second)
	}
	copyDB, err := store.Open(filepath.Join(second[0], "space.db"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := copyDB.ListTasks(context.Background())
	_ = copyDB.Close()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("renewed snapshot tasks: %d, %v", len(tasks), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.key"), make([]byte, 32), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runBackup(dir, 7); err == nil {
		t.Fatal("mismatched key accepted")
	}
	last := visibleBundles(t, dir)
	if len(last) != 1 || last[0] != second[0] {
		t.Fatalf("prior bundle lost: %v", last)
	}
}

func TestPruneBundlesKeepsNewestGenerationForSevenDays(t *testing.T) {
	dir := t.TempDir()
	for day := 1; day <= 8; day++ {
		for generation := 1; generation <= 2; generation++ {
			name := "space-2026-10-" + fmt.Sprintf("%02d", day) + "-00000" + fmt.Sprint(generation) + ".000000000-" + strings.Repeat("a", 31) + fmt.Sprint(generation) + ".bundle"
			if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := pruneBundles(dir, 7); err != nil {
		t.Fatal(err)
	}
	items, err := filepath.Glob(filepath.Join(dir, "space-*.bundle"))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 7 {
		t.Fatalf("retained %d bundles", len(items))
	}
	for _, item := range items {
		name := filepath.Base(item)
		if strings.Contains(name, "2026-10-01") || strings.Contains(name, "-000001.") {
			t.Fatalf("stale generation retained: %s", name)
		}
	}
}

func TestBackupRejectsCorruptEncryptedSecretWithoutReplacingBundle(t *testing.T) {
	dir, s, _, _ := backupFixture(t)
	if _, err := runBackup(dir, 7); err != nil {
		t.Fatal(err)
	}
	before := visibleBundles(t, dir)
	if len(before) != 1 {
		t.Fatalf("initial bundles: %d", len(before))
	}
	if _, err := s.DB.Exec("UPDATE admin SET totp_nonce=? WHERE id=1", []byte{0}); err != nil {
		t.Fatal(err)
	}
	if _, err := runBackup(dir, 7); err == nil {
		t.Fatal("corrupt encrypted secret accepted")
	}
	after := visibleBundles(t, dir)
	if len(after) != 1 || after[0] != before[0] {
		t.Fatalf("last good bundle lost: %v", after)
	}
}
