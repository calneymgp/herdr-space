package store

import (
	"path/filepath"
	"testing"
)

func TestSQLiteEngineIsApprovedVersion(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var version string
	if e := s.DB.QueryRow("SELECT sqlite_version()").Scan(&version); e != nil {
		t.Fatal(e)
	}
	if version != "3.53.4" {
		t.Fatalf("SQLite engine %s, want 3.53.4", version)
	}
}
