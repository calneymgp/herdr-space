package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"herdr-space/internal/github"
)

// fixtureGitHub is an entirely in-memory transport. Browser checks cannot
// reach GitHub or modify issues in a real repository.
type fixtureGitHub struct {
	mu    sync.Mutex
	items []github.Issue
	next  int
}

func newFixtureGitHub() *fixtureGitHub {
	return &fixtureGitHub{next: 3, items: []github.Issue{
		{Number: 1, Title: "Review GitHub integration", Body: "Synthetic issue description", State: github.State("open"), URL: "https://github.com/herdr-fixture/workspace/issues/1", UpdatedAt: time.Now().UTC().Format(time.RFC3339)},
		{Number: 2, Title: "Validate closed issue", Body: "Isolated test", State: github.State("closed"), URL: "https://github.com/herdr-fixture/workspace/issues/2", UpdatedAt: time.Now().UTC().Format(time.RFC3339)},
	}}
}

func (g *fixtureGitHub) Status(context.Context) github.Status {
	return github.Status{Available: true, Authenticated: true}
}

func (g *fixtureGitHub) ListIssues(_ context.Context, repository string, state github.State, page int) (github.IssuePage, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if repository != "herdr-fixture/workspace" {
		return github.IssuePage{}, errors.New("fixture repository mismatch")
	}
	items := []github.Issue{}
	for _, issue := range g.items {
		if state == github.State("all") || issue.State == state {
			items = append(items, issue)
		}
	}
	start := (page - 1) * 50
	if start > len(items) {
		start = len(items)
	}
	end := start + 50
	if end > len(items) {
		end = len(items)
	}
	return github.IssuePage{Items: items[start:end], Page: page, HasMore: end < len(items)}, nil
}

func (g *fixtureGitHub) CreateIssue(_ context.Context, repository, title, body string) (github.Issue, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if repository != "herdr-fixture/workspace" {
		return github.Issue{}, errors.New("fixture repository mismatch")
	}
	issue := github.Issue{Number: g.next, Title: title, Body: body, State: github.State("open"), URL: fmt.Sprintf("https://github.com/herdr-fixture/workspace/issues/%d", g.next), UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	g.next++
	g.items = append(g.items, issue)
	return issue, nil
}

func (g *fixtureGitHub) UpdateIssue(_ context.Context, repository string, number int, update github.IssueUpdate) (github.Issue, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if repository != "herdr-fixture/workspace" {
		return github.Issue{}, errors.New("fixture repository mismatch")
	}
	for index, issue := range g.items {
		if issue.Number != number {
			continue
		}
		if update.Title != nil {
			issue.Title = *update.Title
		}
		if update.Body != nil {
			issue.Body = *update.Body
		}
		if update.State != nil {
			issue.State = *update.State
		}
		issue.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		g.items[index] = issue
		return issue, nil
	}
	return github.Issue{}, errors.New("fixture issue not found")
}
