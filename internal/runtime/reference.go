package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"herdr-space/internal/agents"
	"herdr-space/internal/model"
)

func hasCapability(s model.Session, capability string) bool {
	for _, c := range s.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}
func (m *Manager) SetReferenceStore(ctx context.Context, store model.ReferenceStore) error {
	if store == nil {
		return errors.New("reference store missing")
	}
	refs, err := store.LoadRuntimeReferences(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refStore = store
	for _, r := range refs {
		if !m.validReference(r) {
			continue
		}
		s := r.Session
		s.Alive = false
		s.Capabilities = nil
		s.Activity = "unknown"
		m.stopped[s.ID] = entry{session: s, socket: r.Socket, process: agents.Match{PID: s.PID, StartTime: s.StartTime}, agentRef: r.AgentRef}
	}
	return nil
}
func (m *Manager) validReference(r model.RuntimeReference) bool {
	s := r.Session
	if s.Source != "herdr" || s.Membership != "managed" || s.TerminalID == "" || s.ID != socketID(r.Socket)+":"+s.TerminalID || s.PID <= 0 || s.StartTime == "" || r.AgentRef == "" {
		return false
	}
	if _, err := resumeArgs(s.Agent, r.AgentRef); err != nil {
		return false
	}
	rel, err := filepath.Rel(m.cfg.HerdrConfigDir, r.Socket)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && filepath.Base(r.Socket) == "herdr.sock"
}
func exited(m *Manager, e entry) bool {
	if e.session.PID <= 0 || e.session.StartTime == "" {
		return false
	}
	now, uid, err := agents.Read(m.cfg.ProcRoot, e.session.PID)
	if err == nil {
		return uid != m.uid || now.StartTime != e.session.StartTime
	}
	_, statErr := os.Stat(filepath.Join(m.cfg.ProcRoot, fmt.Sprint(e.session.PID)))
	return errors.Is(statErr, os.ErrNotExist)
}
func (m *Manager) persist(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.persistGate <- struct{}{}:
	}
	defer func() { <-m.persistGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	store := m.refStore
	if store == nil {
		m.mu.RUnlock()
		return nil
	}
	by := make(map[string]entry, len(m.stopped)+len(m.byID))
	for id, e := range m.stopped {
		if _, consumed := m.consumed[id]; !consumed {
			by[id] = e
		}
	}
	for id, e := range m.byID {
		if _, consumed := m.consumed[id]; e.agentRef != "" && e.session.Alive && !consumed {
			by[id] = e
		}
	}
	refs := make([]model.RuntimeReference, 0, len(by))
	for _, e := range by {
		if e.agentRef == "" {
			continue
		}
		s := e.session
		s.Alive = false
		s.Capabilities = nil
		s.Activity = "unknown"
		r := model.RuntimeReference{Session: s, Socket: e.socket, AgentRef: e.agentRef}
		if m.validReference(r) {
			refs = append(refs, r)
		}
	}
	m.mu.RUnlock()
	sort.Slice(refs, func(i, j int) bool { return refs[i].Session.ID < refs[j].Session.ID })
	saveCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return store.SaveRuntimeReferences(saveCtx, refs)
}
func (m *Manager) persistOrWarn(ctx context.Context) {
	if err := m.persist(ctx); err != nil {
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.inventory.Warning = "Failed to save conversation references"
		m.mu.Unlock()
	}
}
func (m *Manager) historical(s model.Session, e entry) (model.Session, bool) {
	if !exited(m, e) {
		return model.Session{}, false
	}
	if _, err := resumeArgs(s.Agent, e.agentRef); err != nil {
		return model.Session{}, false
	}
	s.Alive = false
	s.Capabilities = []string{"resume"}
	s.Activity = "done"
	return s, true
}
