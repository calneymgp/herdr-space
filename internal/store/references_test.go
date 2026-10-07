package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"herdr-space/internal/model"
)

func TestRuntimeReferencesSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	ref := model.RuntimeReference{Session: model.Session{ID: "session-1", Name: "Codex", Source: "herdr", Agent: "codex", Launcher: "codex", ProjectID: "project-1", ServerID: "server-1", TerminalID: "terminal-1", WorkspaceID: "workspace-1", PaneID: "pane-1", PID: 123, StartTime: "42", CWD: "/work", Membership: "managed", Alive: true, UpdatedAt: "2026-10-05T14:00:00Z"}, Socket: "/tmp/herdr.sock", AgentRef: "official-ref"}
	if e := s.SaveRuntimeReferences(context.Background(), []model.RuntimeReference{ref}); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.LoadRuntimeReferences(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 || got[0].Session.ID != ref.Session.ID || got[0].Session.Name != "Codex" || got[0].Session.Source != "herdr" || got[0].Session.Agent != "codex" || got[0].Session.Launcher != "codex" || got[0].Session.ProjectID != "project-1" || got[0].Session.ServerID != "server-1" || got[0].Session.TerminalID != "terminal-1" || got[0].Session.WorkspaceID != "workspace-1" || got[0].Session.PaneID != "pane-1" || got[0].Session.PID != 123 || got[0].Session.StartTime != "42" || got[0].Session.CWD != "/work" || got[0].Session.Membership != "managed" || !got[0].Session.Alive || got[0].Session.UpdatedAt != "2026-10-05T14:00:00Z" || got[0].AgentRef != "official-ref" || got[0].Socket != "/tmp/herdr.sock" {
		t.Fatalf("restored %+v", got)
	}
	if e := s.SaveRuntimeReferences(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	got, e = s.LoadRuntimeReferences(context.Background())
	if e != nil || len(got) != 0 {
		t.Fatalf("replace-all %+v %v", got, e)
	}
}
func TestVersionOneUpgradeHasPreupgradeBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE legacy(value TEXT); INSERT INTO legacy VALUES('keep'); PRAGMA user_version=1"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var version int
	if e := s.DB.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version != 2 {
		t.Fatalf("version %d %v", version, e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	backups := 0
	for _, item := range entries {
		if strings.HasPrefix(item.Name(), "app.db.pre-upgrade-") {
			backups++
			prior, e := sql.Open("sqlite", filepath.Join(dir, item.Name()))
			if e != nil {
				t.Fatal(e)
			}
			var old int
			e = prior.QueryRow("PRAGMA user_version").Scan(&old)
			prior.Close()
			if e != nil || old != 1 {
				t.Fatalf("backup version %d %v", old, e)
			}
		}
	}
	if backups != 1 {
		t.Fatalf("preupgrade backups %d", backups)
	}
}
func TestRuntimeReferenceRejectsIncompleteMetadataWithoutReplacingExisting(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	good := model.RuntimeReference{Session: model.Session{ID: "one"}, Socket: "/tmp/herdr.sock", AgentRef: "ref"}
	if e := s.SaveRuntimeReferences(context.Background(), []model.RuntimeReference{good}); e != nil {
		t.Fatal(e)
	}
	if e := s.SaveRuntimeReferences(context.Background(), []model.RuntimeReference{{Session: model.Session{ID: "two"}}}); !errors.Is(e, ErrInvalid) {
		t.Fatalf("invalid save %v", e)
	}
	got, e := s.LoadRuntimeReferences(context.Background())
	if e != nil || len(got) != 1 || got[0].Session.ID != "one" {
		t.Fatalf("existing refs %+v %v", got, e)
	}
}
func TestFailedVersionOneUpgradeKeepsPriorVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.db")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE runtime_references(wrong TEXT); PRAGMA user_version=1"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if s, e := Open(path); e == nil {
		s.Close()
		t.Fatal("malformed old schema upgraded")
	}
	db, e = sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var version int
	if e := db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || version != 1 {
		t.Fatalf("premature version commit %d %v", version, e)
	}
}
