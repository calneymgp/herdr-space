package store

import (
	"context"
	"encoding/json"
	"herdr-space/internal/model"
	"path/filepath"
)

const maxRuntimeReferences = 10000

// SaveRuntimeReferences atomically replaces the small set of proven resume
// references. It stores metadata only; terminal output and argv are excluded.
func (s *Store) SaveRuntimeReferences(ctx context.Context, refs []model.RuntimeReference) error {
	if len(refs) > maxRuntimeReferences {
		return ErrInvalid
	}
	type row struct{ id, json, socket, agentRef string }
	rows := make([]row, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.Session.ID == "" || len(ref.Session.ID) > 256 || !filepath.IsAbs(ref.Socket) || len(ref.Socket) > 4096 || ref.AgentRef == "" || len(ref.AgentRef) > 4096 {
			return ErrInvalid
		}
		if _, ok := seen[ref.Session.ID]; ok {
			return ErrInvalid
		}
		seen[ref.Session.ID] = struct{}{}
		session := model.Session{ID: ref.Session.ID, Name: ref.Session.Name, Source: ref.Session.Source, Agent: ref.Session.Agent, Launcher: ref.Session.Launcher, ProjectID: ref.Session.ProjectID, CWD: ref.Session.CWD, ServerID: ref.Session.ServerID, TerminalID: ref.Session.TerminalID, WorkspaceID: ref.Session.WorkspaceID, PaneID: ref.Session.PaneID, PID: ref.Session.PID, StartTime: ref.Session.StartTime, Membership: ref.Session.Membership, Alive: ref.Session.Alive, UpdatedAt: ref.Session.UpdatedAt}
		b, e := json.Marshal(session)
		if e != nil || len(b) > 16384 {
			return ErrInvalid
		}
		rows = append(rows, row{ref.Session.ID, string(b), ref.Socket, ref.AgentRef})
	}
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "DELETE FROM runtime_references"); e != nil {
		return e
	}
	stmt, e := tx.PrepareContext(ctx, "INSERT INTO runtime_references(session_id,session_json,socket,agent_ref) VALUES(?,?,?,?)")
	if e != nil {
		return e
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, e = stmt.ExecContext(ctx, row.id, row.json, row.socket, row.agentRef); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) LoadRuntimeReferences(ctx context.Context) ([]model.RuntimeReference, error) {
	rows, e := s.DB.QueryContext(ctx, "SELECT session_id,session_json,socket,agent_ref FROM runtime_references ORDER BY session_id LIMIT 10001")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	refs := []model.RuntimeReference{}
	for rows.Next() {
		if len(refs) >= maxRuntimeReferences {
			return nil, ErrInvalid
		}
		var id, data string
		var ref model.RuntimeReference
		if e := rows.Scan(&id, &data, &ref.Socket, &ref.AgentRef); e != nil {
			return nil, e
		}
		if len(data) > 16384 || json.Unmarshal([]byte(data), &ref.Session) != nil || ref.Session.ID != id || ref.AgentRef == "" || !filepath.IsAbs(ref.Socket) {
			return nil, ErrInvalid
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}
