package agents

import (
	"fmt"
	"os"
	"strconv"
)

// ProcSnapshot is a short-lived view of process ancestry for one inventory scan.
// Candidate identity and agent classification are read again from /proc.
type ProcSnapshot struct {
	root    string
	uid     int
	entries int
	procs   map[int]proc
}

func Snapshot(root string, uid int) (ProcSnapshot, error) {
	return newProcSnapshot(root, uid, os.ReadDir)
}

func newProcSnapshot(root string, uid int, readDir func(string) ([]os.DirEntry, error)) (ProcSnapshot, error) {
	entries, err := readDir(root)
	if err != nil {
		return ProcSnapshot{}, err
	}
	if len(entries) > 65536 {
		return ProcSnapshot{}, fmt.Errorf("proc inventory too large")
	}
	s := ProcSnapshot{root: root, uid: uid, entries: len(entries), procs: make(map[int]proc, len(entries))}
	for _, item := range entries {
		pid, err := strconv.Atoi(item.Name())
		if err != nil || pid <= 0 {
			continue
		}
		m, u, err := Read(root, pid)
		if err == nil {
			s.procs[pid] = proc{Match: m, UID: u}
		}
	}
	return s, nil
}

func (s ProcSnapshot) DetectInPane(shellPID, foregroundGroup int, foreground []int) []Match {
	// Explicit foreground PIDs come from the current RPC. Descendants discovered
	// through the snapshot must retain the same process identity and ancestry.
	candidates := make(map[int]bool, len(foreground))
	for _, pid := range foreground {
		candidates[pid] = true
	}
	if shellPID > 0 && foregroundGroup > 0 && s.entries <= 4096 {
		for pid, m := range s.procs {
			if candidates[pid] || m.UID != s.uid || m.Group != foregroundGroup {
				continue
			}
			inside, valid := s.ancestry(m.PPID, shellPID)
			if !valid || !inside {
				continue
			}
			candidates[pid] = false
		}
	}
	found := map[int]Match{}
	for pid, explicit := range candidates {
		fresh, uid, err := Read(s.root, pid)
		if err != nil || uid != s.uid {
			continue
		}
		if !explicit {
			old := s.procs[pid]
			if fresh.StartTime != old.StartTime || fresh.PPID != old.PPID || fresh.Group != old.Group {
				continue
			}
		}
		fresh.Agent = classify(fresh)
		if fresh.Agent != "" && !fresh.Service && externalVerified(fresh) {
			found[pid] = fresh
		}
	}
	return dedupe(s.root, found)
}

func (s ProcSnapshot) ancestry(pid, ancestor int) (bool, bool) {
	if ancestor <= 0 || pid <= 0 {
		return false, true
	}
	for depth := 0; depth < 64 && pid > 1; depth++ {
		p, ok := s.procs[pid]
		if !ok {
			return false, false
		}
		fresh, uid, err := Read(s.root, pid)
		if err != nil || uid != p.UID || fresh.StartTime != p.StartTime || fresh.PPID != p.PPID {
			return false, false
		}
		if pid == ancestor {
			return true, true
		}
		pid = p.PPID
	}
	return false, pid <= 1
}

// IsDescendant uses the scan's ancestry only for inventory filtering.
func (s ProcSnapshot) IsDescendant(pid, ancestor int) bool {
	inside, valid := s.ancestry(pid, ancestor)
	return inside || !valid
}

func (s ProcSnapshot) List() []Match {
	found := map[int]Match{}
	for pid, p := range s.procs {
		if p.UID != s.uid || p.TTY == 0 || p.Service || !externalVerified(p.Match) {
			continue
		}
		fresh, uid, err := Read(s.root, pid)
		if err == nil && uid == s.uid && fresh.StartTime == p.StartTime && fresh.TTY != 0 && !fresh.Service && externalVerified(fresh) {
			fresh.Agent = classify(fresh)
			found[pid] = fresh
		}
	}
	return dedupe(s.root, found)
}
