package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const MaxResponseBytes = 4 << 20

type Client struct {
	Socket  string
	Timeout time.Duration
}
type Pane struct {
	PaneID                string `json:"pane_id"`
	TerminalID            string `json:"terminal_id"`
	WorkspaceID           string `json:"workspace_id"`
	TabID                 string `json:"tab_id"`
	CWD                   string `json:"cwd"`
	ForegroundCWD         string `json:"foreground_cwd"`
	TerminalTitleStripped string `json:"terminal_title_stripped"`
	Agent                 string `json:"agent"`
	AgentStatus           string `json:"agent_status"`
}
type Workspace struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}
type Tab struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}
type Snapshot struct {
	Version    string      `json:"version"`
	Workspaces []Workspace `json:"workspaces"`
	Tabs       []Tab       `json:"tabs"`
	Panes      []Pane      `json:"panes"`
	Agents     []struct {
		PaneID      string `json:"pane_id"`
		Agent       string `json:"agent"`
		AgentStatus string `json:"agent_status"`
	} `json:"agents"`
}
type Process struct {
	PID  int      `json:"pid"`
	Name string   `json:"name"`
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
}
type ProcessInfo struct {
	PaneID                   string    `json:"pane_id"`
	ShellPID                 int       `json:"shell_pid"`
	ForegroundProcessGroupID int       `json:"foreground_process_group_id"`
	ForegroundProcesses      []Process `json:"foreground_processes"`
}

func (c Client) Call(ctx context.Context, method string, params any, out any) error {
	if c.Socket == "" {
		return errors.New("HERDR socket unavailable")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if timeout > 40*time.Second {
		timeout = 40 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(dctx, "unix", c.Socket)
	if err != nil {
		return fmt.Errorf("HERDR unavailable: %w", err)
	}
	defer conn.Close()
	deadline, _ := dctx.Deadline()
	_ = conn.SetDeadline(deadline)
	go func() { <-dctx.Done(); _ = conn.Close() }()
	req := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params"`
	}{"1", method, params}
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("HERDR write: %w", err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, MaxResponseBytes+1)).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("HERDR read: %w", err)
	}
	if len(line) > MaxResponseBytes {
		return errors.New("HERDR response too large")
	}
	var env struct {
		ID     string          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err = json.Unmarshal(line, &env); err != nil {
		return fmt.Errorf("HERDR invalid response: %w", err)
	}
	if env.ID != "1" {
		return errors.New("HERDR response ID mismatch")
	}
	if len(env.Error) > 0 && string(env.Error) != "null" {
		return fmt.Errorf("HERDR rejected %s", method)
	}
	if len(env.Result) == 0 {
		return errors.New("HERDR empty result")
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}
func (c Client) Snapshot(ctx context.Context) (Snapshot, error) {
	var r struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	err := c.Call(ctx, "session.snapshot", map[string]any{}, &r)
	return r.Snapshot, err
}
func (c Client) ProcessInfo(ctx context.Context, id string) (ProcessInfo, error) {
	var r struct {
		ProcessInfo ProcessInfo `json:"process_info"`
	}
	err := c.Call(ctx, "pane.process_info", map[string]string{"pane_id": id}, &r)
	return r.ProcessInfo, err
}
