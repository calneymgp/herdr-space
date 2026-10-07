package agents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Match struct {
	Agent        string
	Launcher     string
	PID          int
	PPID         int
	Group        int
	TTY          int
	Service      bool
	PiPackage    bool
	CodexPackage bool
	StartTime    string
	CWD          string
	Exe          string
}
type proc struct {
	Match
	UID int
}

func Read(root string, pid int) (Match, int, error) {
	var m Match
	m.PID = pid
	dir := filepath.Join(root, strconv.Itoa(pid))
	stat, e := os.ReadFile(filepath.Join(dir, "stat"))
	if e != nil {
		return m, 0, e
	}
	start := bytes.LastIndexByte(stat, ')')
	if start < 0 {
		return m, 0, fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(string(stat[start+1:]))
	if len(fields) < 20 {
		return m, 0, fmt.Errorf("short proc stat")
	}
	m.PPID, _ = strconv.Atoi(fields[1])
	m.Group, _ = strconv.Atoi(fields[2])
	m.TTY, _ = strconv.Atoi(fields[4])
	m.StartTime = fields[19]
	status, e := os.ReadFile(filepath.Join(dir, "status"))
	if e != nil {
		return m, 0, e
	}
	uid := -1
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				uid, _ = strconv.Atoi(f[1])
			}
			break
		}
	}
	if uid < 0 {
		return m, 0, fmt.Errorf("missing uid")
	}
	args, e := os.ReadFile(filepath.Join(dir, "cmdline"))
	if e == nil && len(args) > 0 {
		parts := bytes.Split(bytes.TrimRight(args, "\x00"), []byte{0})
		m.Launcher = string(parts[0])
		if len(parts) > 1 {
			switch string(parts[1]) {
			case "app-server", "mcp-server", "serve":
				m.Service = true
			}
		}
		if len(parts) > 1 && filepath.IsAbs(string(parts[1])) {
			script, err := filepath.EvalSymlinks(string(parts[1]))
			if err == nil {
				clean := filepath.ToSlash(script)
				m.PiPackage = strings.Contains(clean, "/node_modules/@earendil-works/pi-coding-agent/") && strings.HasSuffix(clean, "/dist/cli.js")
				m.CodexPackage = strings.Contains(clean, "/node_modules/@openai/codex/") && strings.HasSuffix(clean, "/bin/codex.js")
				if m.PiPackage {
					m.Launcher = "pi"
				}
			}
		}
	}
	m.Exe, _ = os.Readlink(filepath.Join(dir, "exe"))
	m.CWD, _ = os.Readlink(filepath.Join(dir, "cwd"))
	return m, uid, nil
}
func classify(m Match) string {
	b := strings.ToLower(filepath.Base(m.Exe))
	launch := strings.ToLower(filepath.Base(m.Launcher))
	if b == "" || b == "." {
		b = launch
	}
	switch {
	case strings.Contains(b, "opencode") || strings.Contains(launch, "opencode"):
		return "opencode"
	case b == "codex" || launch == "codex":
		return "codex"
	case b == "claude" || launch == "claude":
		return "claude"
	case b == "pi" || launch == "pi":
		return "pi"
	}
	return ""
}
func externalVerified(m Match) bool {
	b := strings.ToLower(filepath.Base(m.Exe))
	switch classify(m) {
	case "codex":
		return b == "codex" || (b == "node" && m.CodexPackage)
	case "claude":
		return b == "claude" || (strings.Contains(m.Exe, "/claude/versions/") && strings.ToLower(filepath.Base(m.Launcher)) == "claude")
	case "opencode":
		return b == "opencode" || b == "opencode2" || b == "opencode2-wrapped"
	case "pi":
		return b == "pi" || (b == "node" && m.PiPackage)
	}
	return false
}
func Detect(root string, uid int, pids []int) []Match {
	found := map[int]Match{}
	for _, pid := range pids {
		m, u, e := Read(root, pid)
		if e != nil || u != uid {
			continue
		}
		m.Agent = classify(m)
		if m.Agent != "" && !m.Service && externalVerified(m) {
			found[pid] = m
		}
	}
	return dedupe(root, found)
}

func dedupe(root string, found map[int]Match) []Match {
	out := make([]Match, 0, len(found))
	for pid, m := range found {
		skip := false
		for otherID, other := range found {
			if otherID == pid || other.Agent != m.Agent {
				continue
			}
			parent := other.PPID
			for depth := 0; parent > 1 && depth < 64; depth++ {
				if parent == pid {
					skip = true
					break
				}
				v, _, e := Read(root, parent)
				if e != nil {
					break
				}
				parent = v.PPID
			}
			if skip {
				break
			}
		}
		if !skip {
			out = append(out, m)
		}
	}
	return out
}

// DetectInPane supplements HERDR's foreground list with same-UID descendants
// of its shell that still belong to the reported foreground process group.
func DetectInPane(root string, uid, shellPID, foregroundGroup int, foreground []int) []Match {
	candidates := append([]int(nil), foreground...)
	if shellPID <= 0 || foregroundGroup <= 0 {
		return Detect(root, uid, candidates)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return Detect(root, uid, candidates)
	}
	if len(entries) > 4096 {
		return Detect(root, uid, candidates)
	}
	cache := map[int]Match{}
	for _, item := range entries {
		pid, err := strconv.Atoi(item.Name())
		if err != nil || pid <= 0 {
			continue
		}
		m, u, err := Read(root, pid)
		if err == nil && u == uid {
			cache[pid] = m
		}
	}
	for pid, m := range cache {
		if m.Group != foregroundGroup {
			continue
		}
		parent := m.PPID
		for depth := 0; depth < 64 && parent > 1; depth++ {
			if parent == shellPID {
				candidates = append(candidates, pid)
				break
			}
			p, ok := cache[parent]
			if !ok {
				break
			}
			parent = p.PPID
		}
	}
	return Detect(root, uid, candidates)
}
func List(root string, uid int) ([]Match, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	if len(entries) > 65536 {
		return nil, fmt.Errorf("proc inventory too large")
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid <= 0 {
			continue
		}
		m, u, e := Read(root, pid)
		if e == nil && u == uid && m.TTY != 0 && !m.Service && externalVerified(m) {
			pids = append(pids, pid)
		}
	}
	return Detect(root, uid, pids), nil
}
func IsDescendant(root string, pid, ancestor int) bool {
	if ancestor <= 0 || pid <= 0 {
		return false
	}
	for depth := 0; depth < 64 && pid > 1; depth++ {
		if pid == ancestor {
			return true
		}
		m, _, e := Read(root, pid)
		if e != nil {
			return false
		}
		pid = m.PPID
	}
	return false
}
