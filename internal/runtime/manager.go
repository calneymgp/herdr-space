package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"herdr-space/internal/agents"
	"herdr-space/internal/herdr"
	"herdr-space/internal/model"
)

type Config struct {
	HerdrBinary    string
	HerdrConfigDir string
	ProcRoot       string
	ProjectRoots   []string
	ScanInterval   time.Duration
}
type Manager struct {
	cfg           Config
	phaseObserver func(scanPhases)
	uid           int
	mu            sync.RWMutex
	scanGate      chan struct{}
	watcherWG     sync.WaitGroup
	resumeMu      sync.Mutex
	persistGate   chan struct{}
	lastScan      time.Time
	inventory     model.Inventory
	byID          map[string]entry
	stopped       map[string]entry
	consumed      map[string]entry
	projects      map[string]string
	watchers      map[string]bool
	events        chan struct{}
	refStore      model.ReferenceStore
	cancel        context.CancelFunc
	done          chan struct{}
	closeDone     chan struct{}
}
type scanPhases struct {
	Total, GateWait, SnapshotRPC, ProcessRPC, PaneRPC, ProcSnapshot, Classification, Publication time.Duration
	SnapshotCalls, ProcessCalls, PaneCalls, ProcSnapshots                                        int
	Cached                                                                                       bool
}
type entry struct {
	session  model.Session
	socket   string
	process  agents.Match
	agentRef string
}

var _ model.Provider = (*Manager)(nil)

func New(c Config) (*Manager, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, e
	}
	if c.HerdrBinary == "" {
		c.HerdrBinary = filepath.Join(home, ".local/bin/herdr")
	}
	if c.HerdrConfigDir == "" {
		c.HerdrConfigDir = filepath.Join(home, ".config/herdr")
	}
	if c.ProcRoot == "" {
		c.ProcRoot = "/proc"
	}
	if c.ScanInterval <= 0 {
		c.ScanInterval = 10 * time.Second
	}
	return &Manager{cfg: c, uid: os.Getuid(), scanGate: make(chan struct{}, 1), persistGate: make(chan struct{}, 1), byID: map[string]entry{}, stopped: map[string]entry{}, consumed: map[string]entry{}, projects: map[string]string{}, watchers: map[string]bool{}, events: make(chan struct{}, 1)}, nil
}
func (m *Manager) Start(ctx context.Context) {
	for {
		m.mu.Lock()
		if m.closeDone != nil {
			closed := m.closeDone
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-closed:
				continue
			}
		}
		if m.cancel != nil || ctx.Err() != nil {
			m.mu.Unlock()
			return
		}
		break
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	done := make(chan struct{})
	m.done = done
	m.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(m.cfg.ScanInterval)
		defer ticker.Stop()
		m.scan(ctx, true)
		m.ensureWatchers(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.scan(ctx, true)
				m.ensureWatchers(ctx)
			case <-m.events:
				m.scan(ctx, true)
			}
		}
	}()
}
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closeDone != nil {
		closed := m.closeDone
		m.mu.Unlock()
		<-closed
		return nil
	}
	cancel := m.cancel
	done := m.done
	if cancel == nil {
		m.mu.Unlock()
		return nil
	}
	closed := make(chan struct{})
	m.closeDone = closed
	m.mu.Unlock()
	cancel()
	<-done
	m.watcherWG.Wait()
	m.mu.Lock()
	m.cancel = nil
	m.done = nil
	m.closeDone = nil
	close(closed)
	m.mu.Unlock()
	return nil
}
func (m *Manager) Inventory(ctx context.Context) (model.Inventory, error) {
	if err := m.scan(ctx, false); err != nil {
		return model.Inventory{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return model.Inventory{}, err
	}
	return cloneInventory(m.inventory), nil
}
func cloneInventory(v model.Inventory) model.Inventory {
	v.Items = append([]model.Session(nil), v.Items...)
	v.Spaces = append([]model.Space(nil), v.Spaces...)
	for i := range v.Items {
		v.Items[i].Capabilities = append([]string(nil), v.Items[i].Capabilities...)
	}
	return v
}
func socketID(path string) string { h := sha256.Sum256([]byte(path)); return hex.EncodeToString(h[:8]) }
func (m *Manager) sockets() []string {
	paths := []string{filepath.Join(m.cfg.HerdrConfigDir, "herdr.sock")}
	matches, _ := filepath.Glob(filepath.Join(m.cfg.HerdrConfigDir, "sessions", "*", "herdr.sock"))
	paths = append(paths, matches...)
	return paths
}
func (m *Manager) scan(ctx context.Context, force bool) error {
	totalStarted := time.Now()
	phases := scanPhases{}
	defer func() {
		phases.Total = time.Since(totalStarted)
		if m.phaseObserver != nil {
			m.phaseObserver(phases)
		}
	}()
	select {
	case <-ctx.Done():
		phases.GateWait = time.Since(totalStarted)
		return ctx.Err()
	case m.scanGate <- struct{}{}:
	}
	phases.GateWait = time.Since(totalStarted)
	defer func() { <-m.scanGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !force && !m.lastScan.IsZero() && time.Since(m.lastScan) < m.cfg.ScanInterval {
		phases.Cached = true
		return nil
	}
	scanStarted := time.Now()
	now := time.Now().UTC().Format(time.RFC3339)
	items := []model.Session{}
	spaces := []model.Space{}
	current := map[string]entry{}
	success := 0
	fail := 0
	herdrShells := []int{}
	herdrGroups := []int{}
	var procSnapshot *agents.ProcSnapshot
	var procErr error
	procTried := false
	getProcSnapshot := func() *agents.ProcSnapshot {
		if !procTried {
			procTried = true
			var snapshot agents.ProcSnapshot
			started := time.Now()
			snapshot, procErr = agents.Snapshot(m.cfg.ProcRoot, m.uid)
			phases.ProcSnapshot += time.Since(started)
			phases.ProcSnapshots++
			if procErr != nil {
				fail++
			} else {
				procSnapshot = &snapshot
			}
		}
		return procSnapshot
	}
	for _, sock := range m.sockets() {
		if err := ctx.Err(); err != nil {
			return err
		}
		st, e := os.Stat(sock)
		if e != nil {
			continue
		}
		if st.Mode()&os.ModeSocket == 0 {
			continue
		}
		if v, ok := st.Sys().(*syscall.Stat_t); !ok || int(v.Uid) != m.uid {
			continue
		}
		client := herdr.Client{Socket: sock}
		started := time.Now()
		snapshot, e := client.Snapshot(ctx)
		phases.SnapshotRPC += time.Since(started)
		phases.SnapshotCalls++
		if e != nil {
			fail++
			continue
		}
		success++
		serverID := socketID(sock)
		spaceIndex := map[string]int{}
		for _, workspace := range snapshot.Workspaces {
			if workspace.WorkspaceID == "" {
				continue
			}
			if _, present := spaceIndex[workspace.WorkspaceID]; present {
				continue
			}
			name := strings.TrimSpace(workspace.Label)
			if name == "" {
				name = workspace.WorkspaceID
			}
			spaceIndex[workspace.WorkspaceID] = len(spaces)
			spaces = append(spaces, model.Space{ID: serverID + ":workspace:" + workspace.WorkspaceID, Name: name, ServerID: serverID, WorkspaceID: workspace.WorkspaceID})
		}
		tabNames := map[string]string{}
		for _, tab := range snapshot.Tabs {
			if tab.WorkspaceID != "" && tab.TabID != "" {
				tabNames[tab.WorkspaceID+"\x00"+tab.TabID] = strings.TrimSpace(tab.Label)
			}
		}
		for _, pane := range snapshot.Panes {
			if err := ctx.Err(); err != nil {
				return err
			}
			if index, found := spaceIndex[pane.WorkspaceID]; found && spaces[index].Path == "" {
				spaces[index].Path = pane.CWD
				if spaces[index].Path == "" {
					spaces[index].Path = pane.ForegroundCWD
				}
			}
			if pane.TerminalID == "" || pane.PaneID == "" {
				continue
			}
			id := serverID + ":" + pane.TerminalID
			name := tabNames[pane.WorkspaceID+"\x00"+pane.TabID]
			if name == "" {
				name = strings.TrimSpace(pane.TerminalTitleStripped)
			}
			if name == "" {
				name = pane.Agent
			}
			s := model.Session{ID: id, Name: name, Source: "herdr", Agent: canonicalAgent(pane.Agent), Launcher: canonicalLauncher(pane.Agent), CWD: pane.ForegroundCWD, ServerID: serverID, TerminalID: pane.TerminalID, WorkspaceID: pane.WorkspaceID, PaneID: pane.PaneID, Activity: pane.AgentStatus, Membership: "managed", UpdatedAt: now, Capabilities: []string{"observe"}}
			if s.CWD == "" {
				s.CWD = pane.CWD
			}
			m.mu.RLock()
			projectID := m.projects[id]
			m.mu.RUnlock()
			s.ProjectID = projectID
			started := time.Now()
			info, e := client.ProcessInfo(ctx, pane.PaneID)
			phases.ProcessRPC += time.Since(started)
			phases.ProcessCalls++
			if e == nil {
				if info.ShellPID > 0 {
					herdrShells = append(herdrShells, info.ShellPID)
				}
				if info.ForegroundProcessGroupID > 0 {
					herdrGroups = append(herdrGroups, info.ForegroundProcessGroupID)
				}
				pids := make([]int, 0, len(info.ForegroundProcesses))
				for _, p := range info.ForegroundProcesses {
					pids = append(pids, p.PID)
				}
				var matches []agents.Match
				snapshot := getProcSnapshot()
				classStarted := time.Now()
				if snapshot != nil {
					matches = snapshot.DetectInPane(info.ShellPID, info.ForegroundProcessGroupID, pids)
				}
				phases.Classification += time.Since(classStarted)
				if len(matches) == 1 {
					p := matches[0]
					if info.ForegroundProcessGroupID <= 0 || p.Group == info.ForegroundProcessGroupID {
						s.Alive = true
						s.PID = p.PID
						s.StartTime = p.StartTime
						s.Agent = p.Agent
						s.Launcher = launcher(p)
						s.Capabilities = append(s.Capabilities, "control", "stop")
						if p.CWD != "" {
							s.CWD = p.CWD
						}
						current[id] = entry{session: s, socket: sock, process: p}
					}
				}
			} else {
				fail++
			}
			var detail struct {
				Pane struct {
					AgentSession *struct {
						Source string `json:"source"`
						Agent  string `json:"agent"`
						Kind   string `json:"kind"`
						Value  string `json:"value"`
					} `json:"agent_session"`
				} `json:"pane"`
			}
			started = time.Now()
			paneErr := client.Call(ctx, "pane.get", map[string]string{"pane_id": pane.PaneID}, &detail)
			phases.PaneRPC += time.Since(started)
			phases.PaneCalls++
			if err := paneErr; err != nil {
				fail++
			} else if detail.Pane.AgentSession != nil {
				ref := detail.Pane.AgentSession
				if ref.Source == "herdr:"+s.Agent && ref.Agent == s.Agent && ref.Value != "" && len(ref.Value) <= 4096 {
					s.AgentSessionID = ref.Value
				}
			}
			if s.Agent == "" {
				continue
			}
			if s.Name == "" {
				s.Name = s.Agent
			}
			ent := current[id]
			ent.session = s
			ent.socket = sock
			ent.agentRef = s.AgentSessionID
			current[id] = ent
			items = append(items, s)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !procTried {
		getProcSnapshot()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	classStarted := time.Now()
	if procErr == nil {
		global := procSnapshot.List()
		for _, p := range global {
			if err := ctx.Err(); err != nil {
				return err
			}
			inside := false
			for _, shell := range herdrShells {
				if procSnapshot.IsDescendant(p.PID, shell) {
					inside = true
					break
				}
			}
			if !inside {
				for _, group := range herdrGroups {
					if group == p.Group {
						inside = true
						break
					}
				}
			}
			if inside {
				continue
			}
			id := fmt.Sprintf("external:%d:%d:%s", m.uid, p.PID, p.StartTime)
			s := model.Session{ID: id, Name: p.Agent, Source: "external", Agent: p.Agent, Launcher: launcher(p), CWD: p.CWD, PID: p.PID, StartTime: p.StartTime, Membership: "external", Activity: "unknown", Alive: true, UpdatedAt: now, Capabilities: []string{"stop"}}
			current[id] = entry{session: s, process: p}
			items = append(items, s)
		}
	}
	phases.Classification += time.Since(classStarted)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].ID < spaces[j].ID })
	inv := model.Inventory{Items: items, Spaces: spaces}
	if success == 0 || fail > 0 {
		inv.Stale = true
		inv.Warning = "HERDR unavailable or inventory incomplete"
		for i := range inv.Items {
			inv.Items[i].Capabilities = nil
			inv.Items[i].Activity = "unknown"
		}
	}
	publishStarted := time.Now()
	m.mu.Lock()
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	if inv.Stale && success == 0 && (len(m.inventory.Items) > 0 || len(m.inventory.Spaces) > 0) {
		inv.Items = cloneInventory(m.inventory).Items
		inv.Spaces = cloneInventory(m.inventory).Spaces
		for i := range inv.Items {
			inv.Items[i].Capabilities = nil
			inv.Items[i].Activity = "unknown"
			inv.Items[i].Alive = false
		}
	}
	var nextConsumed, nextStopped map[string]entry
	if !inv.Stale {
		consumed := make(map[string]entry, len(m.consumed))
		for id, old := range m.consumed {
			consumed[id] = old
		}
		stopped := make(map[string]entry, len(m.stopped))
		for id, old := range m.stopped {
			stopped[id] = old
		}
		for id, old := range consumed {
			if next, ok := current[id]; ok && newConversation(old, next) {
				delete(consumed, id)
			}
		}
		for id, old := range stopped {
			if next, ok := current[id]; ok && !keepStopped(old, next) {
				delete(stopped, id)
			}
		}
		for id, prior := range m.byID {
			if _, seen := consumed[id]; seen {
				continue
			}
			if prior.session.Alive && prior.agentRef != "" {
				next, ok := current[id]
				if !ok || !next.session.Alive {
					stopped[id] = prior
				}
			}
		}
		itemIndex := map[string]int{}
		for i := range inv.Items {
			itemIndex[inv.Items[i].ID] = i
		}
		for id, old := range stopped {
			if err := ctx.Err(); err != nil {
				m.mu.Unlock()
				return err
			}
			if _, seen := consumed[id]; seen {
				continue
			}
			history, ok := m.historical(old.session, old)
			if !ok {
				continue
			}
			if index, present := itemIndex[id]; present {
				if !inv.Items[index].Alive {
					inv.Items[index].Capabilities = []string{"observe", "resume"}
				}
			} else {
				inv.Items = append(inv.Items, history)
			}
		}
		sort.Slice(inv.Items, func(i, j int) bool { return inv.Items[i].ID < inv.Items[j].ID })
		nextConsumed, nextStopped = consumed, stopped
	}
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	if !inv.Stale {
		m.consumed = nextConsumed
		m.stopped = nextStopped
	}
	m.inventory = inv
	m.byID = current
	m.lastScan = scanStarted
	m.mu.Unlock()
	phases.Publication += time.Since(publishStarted)
	if !inv.Stale {
		m.persistOrWarn(ctx)
	}
	return ctx.Err()
}
func keepStopped(old, next entry) bool {
	return !next.session.Alive || (old.session.PID == next.session.PID && old.session.StartTime == next.session.StartTime)
}
func canonicalAgent(s string) string {
	v := strings.ToLower(s)
	switch {
	case strings.Contains(v, "codex"):
		return "codex"
	case strings.Contains(v, "claude"):
		return "claude"
	case strings.Contains(v, "opencode"):
		return "opencode"
	case v == "pi":
		return "pi"
	}
	return ""
}
func canonicalLauncher(s string) string {
	if canonicalAgent(s) == "opencode" && strings.Contains(strings.ToLower(s), "2") {
		return "opencode2"
	}
	return canonicalAgent(s)
}
func launcher(m agents.Match) string {
	if m.Agent == "opencode" && strings.Contains(strings.ToLower(m.Launcher), "opencode2") {
		return "opencode2"
	}
	return m.Agent
}
func (m *Manager) current(ctx context.Context, id string) (entry, error) {
	if err := m.scan(ctx, true); err != nil {
		return entry{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return entry{}, err
	}
	if m.inventory.Stale {
		return entry{}, errors.New("HERDR unavailable")
	}
	e, ok := m.byID[id]
	if !ok {
		return entry{}, errors.New("session not found")
	}
	return e, nil
}
func (m *Manager) validateProject(path string) (string, error) {
	if path == "" {
		return "", errors.New("project required")
	}
	real, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", e
	}
	st, e := os.Stat(real)
	if e != nil || !st.IsDir() {
		return "", errors.New("invalid project directory")
	}
	for _, root := range m.cfg.ProjectRoots {
		r, e := filepath.EvalSymlinks(root)
		if e != nil {
			continue
		}
		rel, e := filepath.Rel(r, real)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return real, nil
		}
	}
	return "", fmt.Errorf("project outside allowed roots")
}
func (m *Manager) ensureWatchers(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	for _, sock := range m.sockets() {
		st, e := os.Stat(sock)
		if e != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		v, ok := st.Sys().(*syscall.Stat_t)
		if !ok || int(v.Uid) != m.uid {
			continue
		}
		m.mu.Lock()
		if m.watchers[sock] {
			m.mu.Unlock()
			continue
		}
		m.watchers[sock] = true
		m.watcherWG.Add(1)
		m.mu.Unlock()
		go func(path string) {
			defer m.watcherWG.Done()
			defer func() { m.mu.Lock(); delete(m.watchers, path); m.mu.Unlock() }()
			_ = (herdr.Client{Socket: path}).Subscribe(ctx, func() {
				select {
				case m.events <- struct{}{}:
				default:
				}
			})
		}(sock)
	}
}

func newConversation(old, next entry) bool {
	return next.session.Alive && next.agentRef != "" && next.agentRef != old.agentRef && (next.session.PID != old.session.PID || next.session.StartTime != old.session.StartTime)
}
