package model

import (
	"context"
	"errors"
)

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
}
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	ProjectID   string `json:"project_id"`
	SessionID   string `json:"session_id"`
	Status      string `json:"status"`
	DueDate     string `json:"due_date"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}
type Note struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	ProjectID string `json:"project_id"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
type Preferences struct {
	Theme string `json:"theme"`
}
type Space struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	ServerID    string `json:"server_id"`
	WorkspaceID string `json:"workspace_id"`
}
type Session struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Source         string   `json:"source"`
	Agent          string   `json:"agent"`
	Launcher       string   `json:"launcher"`
	ProjectID      string   `json:"project_id"`
	CWD            string   `json:"cwd"`
	ServerID       string   `json:"server_id"`
	TerminalID     string   `json:"terminal_id"`
	WorkspaceID    string   `json:"workspace_id"`
	PaneID         string   `json:"pane_id"`
	PID            int      `json:"pid"`
	StartTime      string   `json:"start_time"`
	AgentSessionID string   `json:"agent_session_id,omitempty"`
	Membership     string   `json:"membership"`
	Activity       string   `json:"activity"`
	Alive          bool     `json:"alive"`
	UpdatedAt      string   `json:"updated_at"`
	Capabilities   []string `json:"capabilities"`
}
type Inventory struct {
	Items   []Session `json:"items"`
	Spaces  []Space   `json:"spaces"`
	Stale   bool      `json:"stale"`
	Warning string    `json:"warning,omitempty"`
}
type StartRequest struct {
	Agent          string `json:"agent"`
	Launcher       string `json:"launcher"`
	ProjectID      string `json:"project_id"`
	ProjectPath    string `json:"-"`
	AgentSessionID string `json:"-"`
}
type StreamRequest struct {
	ID       string
	Mode     string
	Takeover bool
	Cols     int
	Rows     int
}

var (
	// ErrTerminalStreamSlowConsumer identifies a terminal frame queue that is full.
	ErrTerminalStreamSlowConsumer = errors.New("terminal stream slow consumer")
	// ErrTerminalStreamBridge identifies a malformed or interrupted bridge stream.
	ErrTerminalStreamBridge = errors.New("terminal stream bridge failure")
)

type TerminalStream interface {
	// Frames and Errors are independent channels. Either may close first, and
	// Errors may still contain a buffered error after either channel closes.
	// Producers must queue a final error before closing Frames. Consumers may
	// finish normally when Frames closes after checking buffered Errors.
	Frames() <-chan []byte
	Errors() <-chan error
	Input([]byte) error
	Resize(int, int) error
	Scroll(int) error
	Close() error
}
type Provider interface {
	Inventory(context.Context) (Inventory, error)
	Stop(context.Context, string, bool) error
	Resume(context.Context, string, StartRequest) (Session, error)
	Launch(context.Context, StartRequest) (Session, error)
	Attach(context.Context, StreamRequest) (TerminalStream, error)
}
