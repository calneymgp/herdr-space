package github

import (
	"context"
	"errors"
)

type State string

const (
	StateOpen   State = "open"
	StateClosed State = "closed"
	StateAll    State = "all"
)

type Status struct {
	Available     bool `json:"available"`
	Authenticated bool `json:"authenticated"`
}

type Issue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     State  `json:"state"`
	URL       string `json:"url"`
	UpdatedAt string `json:"updated_at"`
}

type IssuePage struct {
	Items   []Issue `json:"items"`
	Page    int     `json:"page"`
	HasMore bool    `json:"has_more"`
}

// IssueUpdate uses pointers so a state change can leave an issue's edited text
// untouched, and a text edit can leave its current open/closed state untouched.
type IssueUpdate struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
	State *State  `json:"state,omitempty"`
}

type Provider interface {
	Status(context.Context) Status
	ListIssues(context.Context, string, State, int) (IssuePage, error)
	CreateIssue(context.Context, string, string, string) (Issue, error)
	UpdateIssue(context.Context, string, int, IssueUpdate) (Issue, error)
}

var ErrInvalid = errors.New("invalid GitHub request")
