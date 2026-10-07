package persistence_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"herdr-space/internal/model"
	"herdr-space/internal/runtime"
	"herdr-space/internal/store"
)

// Exercises the real store/provider boundary: a memory fake cannot catch a
// field dropped by SQLite serialization. No live HERDR or process is touched.
func TestSQLiteReferenceReloadGrantsOnlyValidatedResume(t *testing.T) {
	ctx := context.Background()
	configDir, procDir := t.TempDir(), t.TempDir()
	socket := filepath.Join(configDir, "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				var request map[string]any
				if json.NewDecoder(connection).Decode(&request) != nil {
					return
				}
				// An empty but available HERDR proves the old pane is gone.
				json.NewEncoder(connection).Encode(map[string]any{"id": request["id"], "result": map[string]any{"snapshot": map[string]any{"panes": []any{}}}})
			}()
		}
	}()
	path := filepath.Join(t.TempDir(), "space.db")
	database, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ref := reference(socket)
	if err := database.SaveRuntimeReferences(ctx, []model.RuntimeReference{ref}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := runtime.New(runtime.Config{HerdrConfigDir: configDir, ProcRoot: procDir})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.SetReferenceStore(ctx, database); err != nil {
		t.Fatal(err)
	}
	inventory, err := manager.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Stale || len(inventory.Items) != 1 {
		t.Fatalf("reference was not restored: stale=%v count=%d", inventory.Stale, len(inventory.Items))
	}
	session := inventory.Items[0]
	if session.Alive || session.Source != "herdr" || session.Name != ref.Session.Name || session.UpdatedAt != ref.Session.UpdatedAt {
		t.Fatal("restored identity or display metadata changed")
	}
	if len(session.Capabilities) != 1 || session.Capabilities[0] != "resume" {
		t.Fatalf("historical capabilities: %v", session.Capabilities)
	}
	if err := manager.Stop(ctx, session.ID, false); err == nil {
		t.Fatal("historical metadata granted termination")
	}
	if stream, err := manager.Attach(ctx, model.StreamRequest{ID: session.ID, Mode: "control", Cols: 80, Rows: 24}); err == nil {
		stream.Close()
		t.Fatal("historical metadata granted control")
	}
}

func reference(socket string) model.RuntimeReference {
	hash := sha256.Sum256([]byte(socket))
	serverID := hex.EncodeToString(hash[:8])
	return model.RuntimeReference{
		Session: model.Session{ID: serverID + ":closed-terminal", Name: "Closed conversation", Source: "herdr", Agent: "codex", Launcher: "codex", ServerID: serverID, TerminalID: "closed-terminal", WorkspaceID: "old-workspace", PaneID: "old-pane", PID: 77, StartTime: "123", Membership: "managed", UpdatedAt: "2026-10-05T12:00:00Z"},
		Socket:  socket, AgentRef: "verified-conversation",
	}
}
