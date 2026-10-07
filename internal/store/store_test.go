package store

import (
	"context"
	"errors"
	"fmt"
	"herdr-space/internal/model"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNoteConflictPreservesBody(t *testing.T) {
	dir := t.TempDir()
	fi, _ := os.Stat(dir)
	t.Logf("dir mode %o", fi.Mode().Perm())
	s, err := Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	n, err := s.CreateNote(ctx, model.Note{Title: "one", Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	n.Body = "second"
	saved, err := s.UpdateNote(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	n.Body = "stale"
	current, err := s.UpdateNote(ctx, n)
	if !errors.Is(err, ErrConflict) || current.Body != "second" || current.Version != saved.Version {
		t.Fatalf("conflict=%v current=%+v", err, current)
	}
}
func TestTaskRejectsUnknownProject(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateTask(context.Background(), model.Task{Title: "work", Status: "todo", ProjectID: "missing"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("error=%v", err)
	}
}

func TestGetOrCreateProjectIsIdempotentAcrossStoreConnections(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "app.db")
	seed, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	type result struct {
		project model.Project
		err     error
	}
	for i := 0; i < 64; i++ {
		path := filepath.Join(dir, fmt.Sprintf("space-%d", i))
		start := make(chan struct{})
		results := make(chan result, 2)
		var wait sync.WaitGroup
		for _, db := range []*Store{first, second} {
			wait.Add(1)
			go func(db *Store) {
				defer wait.Done()
				<-start
				project, err := db.GetOrCreateProject(context.Background(), model.Project{Name: "Workspace", Path: path})
				results <- result{project: project, err: err}
			}(db)
		}
		close(start)
		wait.Wait()
		close(results)
		var ids []string
		for got := range results {
			if got.err != nil {
				t.Fatalf("concurrent get-or-create failed: %v", got.err)
			}
			ids = append(ids, got.project.ID)
		}
		if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
			t.Fatalf("path %q resolved to multiple projects: %v", path, ids)
		}
	}
}
func TestBackupRestoresCommittedRows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	_, err = s.CreateProject(ctx, model.Project{Name: "A", Path: "/tmp/a"})
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.db")
	if err := s.Backup(backup); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.Integrity(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := restored.ListProjects(ctx)
	if err != nil || len(items) != 1 || items[0].Name != "A" {
		t.Fatalf("restored %+v, %v", items, err)
	}
}
func TestDataFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(filepath.Join(dir, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for _, name := range []string{dir, filepath.Join(dir, "app.db")} {
		info, e := os.Stat(name)
		if e != nil {
			t.Fatal(e)
		}
		want := os.FileMode(0700)
		if name != dir {
			want = 0600
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode %o", name, info.Mode().Perm())
		}
	}
}
