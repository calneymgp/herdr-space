package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"herdr-space/internal/agents"
	"herdr-space/internal/herdr"
	"herdr-space/internal/model"
)

func (m *Manager) Stop(ctx context.Context, id string, force bool) error {
	e, err := m.current(ctx, id)
	if err != nil {
		return err
	}
	if !e.session.Alive || e.session.PID <= 0 {
		return errors.New("unverified process")
	}
	if e.session.Membership == "managed" {
		client := herdr.Client{Socket: e.socket}
		var detail struct {
			Pane struct {
				TerminalID string `json:"terminal_id"`
			} `json:"pane"`
		}
		if err := client.Call(ctx, "pane.get", map[string]string{"pane_id": e.session.PaneID}, &detail); err != nil || detail.Pane.TerminalID != e.session.TerminalID {
			return errors.New("terminal identity changed")
		}
		info, err := client.ProcessInfo(ctx, e.session.PaneID)
		if err != nil {
			return errors.New("terminal process unavailable")
		}
		pids := make([]int, 0, len(info.ForegroundProcesses))
		for _, p := range info.ForegroundProcesses {
			pids = append(pids, p.PID)
		}
		matches := agents.DetectInPane(m.cfg.ProcRoot, m.uid, info.ShellPID, info.ForegroundProcessGroupID, pids)
		if len(matches) != 1 || matches[0].PID != e.session.PID || matches[0].StartTime != e.session.StartTime || matches[0].Group != e.process.Group || matches[0].Exe != e.process.Exe {
			return errors.New("process identity changed")
		}
		if info.ForegroundProcessGroupID > 0 && matches[0].Group != info.ForegroundProcessGroupID {
			return errors.New("process group changed")
		}
		latest, uid, err := agents.Read(m.cfg.ProcRoot, e.session.PID)
		if err != nil || uid != m.uid || latest.StartTime != e.session.StartTime || latest.Exe != e.process.Exe || latest.Group != e.process.Group {
			return errors.New("process identity changed")
		}
		if err := client.Call(ctx, "pane.close", map[string]string{"pane_id": e.session.PaneID}, nil); err != nil {
			return err
		}
		if e.agentRef != "" {
			m.mu.Lock()
			m.stopped[id] = e
			m.mu.Unlock()
			m.persistOrWarn(ctx)
		}
		return nil
	}
	fd, err := unix.PidfdOpen(e.session.PID, 0)
	if err != nil {
		return fmt.Errorf("process unavailable: %w", err)
	}
	defer unix.Close(fd)
	now, uid, err := agents.Read(m.cfg.ProcRoot, e.session.PID)
	if err != nil || uid != m.uid || now.StartTime != e.process.StartTime || now.Exe != e.process.Exe || now.Group != e.process.Group {
		return errors.New("process identity changed")
	}
	sig := unix.SIGTERM
	if force {
		sig = unix.SIGKILL
	}
	if err = unix.PidfdSendSignal(fd, sig, nil, 0); err != nil {
		return fmt.Errorf("signal not delivered: %w", err)
	}
	m.mu.Lock()
	m.stopped[id] = e
	m.mu.Unlock()
	return nil
}
func selectedAgent(r model.StartRequest) (string, string, error) {
	agent := strings.ToLower(r.Agent)
	launch := strings.ToLower(r.Launcher)
	if launch == "" {
		launch = agent
	}
	switch launch {
	case "codex", "claude", "pi":
		if agent != "" && agent != launch {
			return "", "", errors.New("incompatible agent and launcher")
		}
		return launch, launch, nil
	case "opencode", "opencode2":
		if agent != "" && agent != "opencode" {
			return "", "", errors.New("incompatible agent and launcher")
		}
		return "opencode", launch, nil
	}
	return "", "", errors.New("launcher not allowed")
}
func (m *Manager) launch(ctx context.Context, r model.StartRequest) (model.Session, error) {
	agent, launch, err := selectedAgent(r)
	if err != nil {
		return model.Session{}, err
	}
	args := []string{}
	if r.AgentSessionID != "" {
		args, err = resumeArgs(agent, r.AgentSessionID)
		if err != nil {
			return model.Session{}, err
		}
	}
	project, err := m.validateProject(r.ProjectPath)
	if err != nil {
		return model.Session{}, err
	}
	sock := ""
	for _, p := range m.sockets() {
		st, e := os.Stat(p)
		if e == nil && st.Mode()&os.ModeSocket != 0 {
			if v, ok := st.Sys().(*syscall.Stat_t); ok && int(v.Uid) == m.uid {
				sock = p
				break
			}
		}
	}
	if sock == "" {
		return model.Session{}, errors.New("HERDR unavailable")
	}
	client := herdr.Client{Socket: sock, Timeout: 5 * time.Second}
	var created struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			PaneID     string `json:"pane_id"`
			TerminalID string `json:"terminal_id"`
		} `json:"root_pane"`
	}
	if err = client.Call(ctx, "workspace.create", map[string]any{"cwd": project, "focus": false, "label": filepath.Base(project)}, &created); err != nil {
		return model.Session{}, &model.LaunchError{PublicMessage: "Could not confirm workspace creation; check HERDR before trying again.", Cause: err}
	}
	if created.RootPane.PaneID == "" {
		return model.Session{}, &model.LaunchError{WorkspaceID: created.Workspace.WorkspaceID, PublicMessage: "Workspace created; check the pane status in HERDR before trying again.", Cause: errors.New("HERDR did not return a pane")}
	}
	var started any
	if launch == "opencode2" {
		err = client.Call(ctx, "pane.send_input", map[string]any{"pane_id": created.RootPane.PaneID, "text": "opencode2", "keys": []string{"Enter"}}, &started)
	} else {
		client.Timeout = 35 * time.Second
		err = client.Call(ctx, "agent.start", map[string]any{"name": launch, "kind": agent, "pane_id": created.RootPane.PaneID, "args": args, "timeout_ms": uint64(30000)}, &started)
	}
	if err != nil {
		return model.Session{}, &model.LaunchError{WorkspaceID: created.Workspace.WorkspaceID, PublicMessage: "Could not confirm the agent in the created workspace; inspect HERDR before trying again.", Cause: err}
	}
	if err := m.scan(ctx, true); err != nil {
		return model.Session{}, &model.LaunchError{WorkspaceID: created.Workspace.WorkspaceID, PublicMessage: "Agent started, but the inventory could not be confirmed; check the workspace in HERDR.", Cause: err}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.byID {
		if e.socket == sock && e.session.PaneID == created.RootPane.PaneID {
			m.projects[id] = r.ProjectID
			e.session.Membership = "managed"
			e.session.ProjectID = r.ProjectID
			e.session.Launcher = launch
			m.byID[id] = e
			for i := range m.inventory.Items {
				if m.inventory.Items[i].ID == id {
					m.inventory.Items[i] = e.session
				}
			}
			return e.session, nil
		}
	}
	if created.RootPane.TerminalID == "" {
		return model.Session{}, &model.LaunchError{WorkspaceID: created.Workspace.WorkspaceID, PublicMessage: "Agent started, but the terminal has not appeared in the inventory; check the workspace in HERDR.", Cause: errors.New("missing terminal")}
	}
	id := socketID(sock) + ":" + created.RootPane.TerminalID
	m.projects[id] = r.ProjectID
	return model.Session{ID: id, Source: "herdr", Agent: agent, Launcher: launch, ProjectID: r.ProjectID, CWD: project, ServerID: socketID(sock), TerminalID: created.RootPane.TerminalID, WorkspaceID: created.Workspace.WorkspaceID, PaneID: created.RootPane.PaneID, Membership: "managed", Activity: "unknown", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}
func (m *Manager) Launch(ctx context.Context, r model.StartRequest) (model.Session, error) {
	if r.AgentSessionID != "" {
		return model.Session{}, errors.New("resume reference not allowed at startup")
	}
	return m.launch(ctx, r)
}
func (m *Manager) Resume(ctx context.Context, id string, r model.StartRequest) (model.Session, error) {
	m.resumeMu.Lock()
	defer m.resumeMu.Unlock()
	if err := m.scan(ctx, true); err != nil {
		return model.Session{}, err
	}
	m.mu.RLock()
	e, ok := m.stopped[id]
	stale := m.inventory.Stale
	_, consumed := m.consumed[id]
	activeSameRef := false
	for _, current := range m.byID {
		if current.session.Alive && current.agentRef != "" && current.agentRef == e.agentRef && current.session.Agent == e.session.Agent {
			activeSameRef = true
			break
		}
	}
	m.mu.RUnlock()
	if stale {
		return model.Session{}, errors.New("inventory unavailable")
	}
	if !ok || e.agentRef == "" || consumed {
		return model.Session{}, errors.New("unverified conversation reference")
	}
	if !exited(m, e) {
		return model.Session{}, errors.New("process exit not yet verified")
	}
	if activeSameRef {
		return model.Session{}, errors.New("conversation is already active")
	}
	r.Agent = e.session.Agent
	r.Launcher = e.session.Launcher
	r.AgentSessionID = e.agentRef
	if _, _, err := selectedAgent(r); err != nil {
		return model.Session{}, err
	}
	if _, err := resumeArgs(r.Agent, r.AgentSessionID); err != nil {
		return model.Session{}, err
	}
	if _, err := m.validateProject(r.ProjectPath); err != nil {
		return model.Session{}, err
	}
	m.mu.Lock()
	reserved := map[string]entry{}
	for otherID, candidate := range m.stopped {
		if candidate.session.Agent == e.session.Agent && candidate.agentRef == e.agentRef {
			reserved[otherID] = candidate
			m.consumed[otherID] = candidate
			delete(m.stopped, otherID)
		}
	}
	for i := range m.inventory.Items {
		if _, ok := reserved[m.inventory.Items[i].ID]; ok {
			m.inventory.Items[i].Capabilities = nil
		}
	}
	m.mu.Unlock()
	if err := m.persist(ctx); err != nil {
		m.mu.Lock()
		for otherID, candidate := range reserved {
			delete(m.consumed, otherID)
			m.stopped[otherID] = candidate
		}
		for i := range m.inventory.Items {
			if _, ok := reserved[m.inventory.Items[i].ID]; ok && !m.inventory.Items[i].Alive {
				m.inventory.Items[i].Capabilities = []string{"resume"}
			}
		}
		m.mu.Unlock()
		return model.Session{}, errors.New("could not reserve the conversation reference")
	}
	started, launchErr := m.launch(ctx, r)
	if launchErr != nil {
		var uncertain *model.LaunchError
		if !errors.As(launchErr, &uncertain) {
			m.mu.Lock()
			for otherID, candidate := range reserved {
				delete(m.consumed, otherID)
				m.stopped[otherID] = candidate
			}
			m.mu.Unlock()
			m.persistOrWarn(ctx)
		}
		return model.Session{}, launchErr
	}
	return started, nil
}

func resumeArgs(agent, ref string) ([]string, error) {
	if ref == "" || len(ref) > 4096 {
		return nil, errors.New("invalid conversation reference")
	}
	switch agent {
	case "codex":
		return []string{"resume", ref}, nil
	case "claude":
		return []string{"--resume", ref}, nil
	case "pi":
		return []string{"--session", ref}, nil
	}
	return nil, errors.New("resume unavailable for this agent")
}
