package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	githubProvider "herdr-space/internal/github"
	"herdr-space/internal/model"
	"herdr-space/internal/store"
)

const (
	maxGitHubSpaces     = 100
	maxGitHubPage       = 10000
	gitDiscoveryWorkers = 8
)

var (
	errGitHubUnavailable = errors.New("GitHub integration unavailable")
	errGitHubRepository  = errors.New("GitHub repository unavailable")
)

func (s *Server) githubStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	status := s.github.Status(ctx)
	result := map[string]any{"available": status.Available, "authenticated": status.Authenticated}
	if !status.Available {
		result["message"] = "GitHub integration is unavailable in this environment."
	} else if !status.Authenticated {
		result["message"] = "GitHub CLI is not authenticated in this environment."
	}
	jsonout(w, 200, result)
}

func (s *Server) currentInventory(ctx context.Context) (model.Inventory, error) {
	if s.Provider == nil {
		return model.Inventory{}, errGitHubUnavailable
	}
	inventory, err := s.Provider.Inventory(ctx)
	if err != nil || inventory.Stale {
		return model.Inventory{}, errGitHubUnavailable
	}
	return inventory, nil
}

func (s *Server) findCurrentSpace(ctx context.Context, id string) (model.Space, error) {
	inventory, err := s.currentInventory(ctx)
	if err != nil {
		return model.Space{}, err
	}
	for _, space := range inventory.Spaces {
		if space.ID == id && space.ID != "" && space.ServerID != "" && space.WorkspaceID != "" {
			return space, nil
		}
	}
	return model.Space{}, store.ErrNotFound
}

func (s *Server) githubRepositories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	inventory, err := s.currentInventory(ctx)
	if err != nil {
		failure(w, 503)
		return
	}
	if len(inventory.Spaces) > maxGitHubSpaces {
		failure(w, 503)
		return
	}
	type repositoryItem struct {
		SpaceID    string `json:"space_id"`
		SpaceName  string `json:"space_name"`
		Repository string `json:"repository"`
	}
	// Each worker writes only its inventory index; collecting afterward keeps
	// response order stable even when git commands finish out of order.
	resolved := make([]repositoryItem, len(inventory.Spaces))
	valid := make([]bool, len(inventory.Spaces))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(gitDiscoveryWorkers, len(inventory.Spaces)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				space := inventory.Spaces[index]
				if space.ID == "" || space.ServerID == "" || space.WorkspaceID == "" {
					continue
				}
				select {
				case s.gitDiscoverySlots <- struct{}{}:
				case <-ctx.Done():
					return
				}
				repository, err := s.githubRepositoryForSpace(ctx, space)
				<-s.gitDiscoverySlots
				if err == nil {
					resolved[index] = repositoryItem{SpaceID: space.ID, SpaceName: space.Name, Repository: repository}
					valid[index] = true
				}
			}
		}()
	}
dispatch:
	for index := range inventory.Spaces {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		failure(w, 503)
		return
	}
	items := make([]repositoryItem, 0)
	for index := range resolved {
		if valid[index] {
			items = append(items, resolved[index])
		}
	}
	jsonout(w, 200, map[string]any{"items": items})
}

func (s *Server) resolveGitHubSpace(ctx context.Context, id string) (model.Space, string, error) {
	space, err := s.findCurrentSpace(ctx, id)
	if err != nil {
		return model.Space{}, "", err
	}
	repository, err := s.githubRepositoryForSpace(ctx, space)
	if err != nil {
		if ctx.Err() != nil {
			return model.Space{}, "", errGitHubUnavailable
		}
		return model.Space{}, "", errGitHubRepository
	}
	return space, repository, nil
}

func (s *Server) githubRepositoryForSpace(ctx context.Context, space model.Space) (string, error) {
	spacePath, err := s.allowedDirectory(space.Path)
	if err != nil {
		return "", errGitHubRepository
	}
	rootOutput, err := runGit(ctx, spacePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errGitHubRepository
	}
	root, err := s.allowedDirectory(strings.TrimSpace(string(rootOutput)))
	if err != nil {
		return "", errGitHubRepository
	}
	remoteOutput, err := runGit(ctx, root, "config", "--local", "--get", "remote.origin.url")
	if err != nil {
		return "", errGitHubRepository
	}
	repository, ok := githubProvider.RepositoryFromRemote(strings.TrimSpace(string(remoteOutput)))
	if !ok {
		return "", errGitHubRepository
	}
	return repository, nil
}

func (s *Server) allowedDirectory(path string) (string, error) {
	if path == "" || len(path) > 4096 || !filepath.IsAbs(path) {
		return "", store.ErrInvalid
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", store.ErrInvalid
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", store.ErrInvalid
	}
	for _, configuredRoot := range s.C.ProjectRoots {
		root, err := filepath.EvalSymlinks(configuredRoot)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, resolved)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", store.ErrInvalid
}

type commandOutput struct {
	bytes.Buffer
	limit   int
	cancel  context.CancelFunc
	tooLong bool
}

func (b *commandOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.tooLong = true
		b.cancel()
		return 0, errors.New("command output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func runGit(ctx context.Context, directory string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", append([]string{"-C", directory}, args...)...)
	cmd.WaitDelay = 500 * time.Millisecond
	stdout := &commandOutput{limit: 16 << 10, cancel: cancel}
	stderr := &commandOutput{limit: 16 << 10, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil || stdout.tooLong || stderr.tooLong || commandCtx.Err() != nil {
		return nil, errGitHubRepository
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func (s *Server) githubIssueList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	spaceIDs := query["space_id"]
	stateValues := query["state"]
	pageValues := query["page"]
	if len(spaceIDs) != 1 || len(spaceIDs[0]) > 256 || len(stateValues) > 1 || len(pageValues) > 1 {
		failure(w, 400)
		return
	}
	state := githubProvider.StateOpen
	if len(stateValues) == 1 && stateValues[0] != "" {
		state = githubProvider.State(stateValues[0])
	}
	if state != githubProvider.StateOpen && state != githubProvider.StateClosed && state != githubProvider.StateAll {
		failure(w, 400)
		return
	}
	page := 1
	if len(pageValues) == 1 && pageValues[0] != "" {
		parsed, err := strconv.Atoi(pageValues[0])
		if err != nil || parsed < 1 || parsed > maxGitHubPage {
			failure(w, 400)
			return
		}
		page = parsed
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, repository, err := s.resolveGitHubSpace(ctx, spaceIDs[0])
	if err != nil {
		githubFailure(w, err)
		return
	}
	result, err := s.github.ListIssues(ctx, repository, state, page)
	if err != nil {
		failure(w, 503)
		return
	}
	result.Page = page
	if result.Items == nil {
		result.Items = []githubProvider.Issue{}
	}
	if len(result.Items) > 50 {
		result.Items = result.Items[:50]
		result.HasMore = true
	}
	items := result.Items[:0]
	for _, issue := range result.Items {
		if issue.Number <= 0 || issue.State != githubProvider.StateOpen && issue.State != githubProvider.StateClosed {
			continue
		}
		issue.URL = "https://github.com/" + repository + "/issues/" + strconv.Itoa(issue.Number)
		items = append(items, issue)
	}
	result.Items = items
	jsonout(w, 200, result)
}

type createIssueRequest struct {
	SpaceID string `json:"space_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

func validIssueText(title, body string) bool {
	return strings.TrimSpace(title) != "" && len(title) <= 256 && len(body) <= 65536
}

func (s *Server) githubIssueCreate(w http.ResponseWriter, r *http.Request) {
	var input createIssueRequest
	if decode(r, &input) != nil || len(input.SpaceID) > 256 || !validIssueText(input.Title, input.Body) {
		failure(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, repository, err := s.resolveGitHubSpace(ctx, input.SpaceID)
	if err != nil {
		githubFailure(w, err)
		return
	}
	issue, err := s.github.CreateIssue(ctx, repository, input.Title, input.Body)
	if err != nil {
		failure(w, 503)
		return
	}
	if issue.Number <= 0 || issue.State != githubProvider.StateOpen && issue.State != githubProvider.StateClosed {
		failure(w, 503)
		return
	}
	issue.URL = "https://github.com/" + repository + "/issues/" + strconv.Itoa(issue.Number)
	jsonout(w, http.StatusCreated, map[string]any{"item": issue})
}

type updateIssueRequest struct {
	SpaceID string                `json:"space_id"`
	Title   *string               `json:"title,omitempty"`
	Body    *string               `json:"body,omitempty"`
	State   *githubProvider.State `json:"state,omitempty"`
}

func (s *Server) githubIssueUpdate(w http.ResponseWriter, r *http.Request, numberText string) {
	if numberText == "" || strings.Contains(numberText, "/") {
		failure(w, 404)
		return
	}
	number, err := strconv.Atoi(numberText)
	if err != nil || number < 1 {
		failure(w, 400)
		return
	}
	var input updateIssueRequest
	if decode(r, &input) != nil || len(input.SpaceID) > 256 {
		failure(w, 400)
		return
	}
	if input.Title == nil && input.Body == nil && input.State == nil || input.Title != nil && (strings.TrimSpace(*input.Title) == "" || len(*input.Title) > 256) || input.Body != nil && len(*input.Body) > 65536 || input.State != nil && *input.State != githubProvider.StateOpen && *input.State != githubProvider.StateClosed {
		failure(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	_, repository, err := s.resolveGitHubSpace(ctx, input.SpaceID)
	if err != nil {
		githubFailure(w, err)
		return
	}
	issue, err := s.github.UpdateIssue(ctx, repository, number, githubProvider.IssueUpdate{Title: input.Title, Body: input.Body, State: input.State})
	if err != nil {
		failure(w, 503)
		return
	}
	if issue.Number != number || issue.Number <= 0 || issue.State != githubProvider.StateOpen && issue.State != githubProvider.StateClosed {
		failure(w, 503)
		return
	}
	issue.URL = "https://github.com/" + repository + "/issues/" + strconv.Itoa(issue.Number)
	jsonout(w, 200, map[string]any{"item": issue})
}

func githubFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalid), errors.Is(err, githubProvider.ErrInvalid):
		failure(w, 400)
	case errors.Is(err, store.ErrNotFound):
		failure(w, 404)
	default:
		failure(w, 503)
	}
}

func (s *Server) spaceProject(w http.ResponseWriter, r *http.Request) {
	const prefix = "/api/v1/spaces/"
	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, prefix) || !strings.HasSuffix(escapedPath, "/project") {
		failure(w, 404)
		return
	}
	encodedID := strings.TrimSuffix(strings.TrimPrefix(escapedPath, prefix), "/project")
	spaceID, err := url.PathUnescape(encodedID)
	if err != nil || spaceID == "" || len(spaceID) > 256 || strings.ContainsRune(spaceID, '\x00') {
		failure(w, 400)
		return
	}
	var empty struct{}
	if decode(r, &empty) != nil {
		failure(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	space, err := s.findCurrentSpace(ctx, spaceID)
	if err != nil {
		githubFailure(w, err)
		return
	}
	projectPath, err := s.allowedDirectory(space.Path)
	if err != nil {
		failure(w, 400)
		return
	}
	name := strings.TrimSpace(space.Name)
	if name == "" {
		name = filepath.Base(projectPath)
	}
	if len(name) > 160 {
		name = filepath.Base(projectPath)
	}
	for len(name) > 160 {
		runes := []rune(name)
		name = string(runes[:len(runes)-1])
	}
	project, err := s.Store.GetOrCreateProject(ctx, model.Project{Name: name, Path: projectPath})
	if err != nil {
		resultErr(w, err)
		return
	}
	jsonout(w, 200, map[string]any{"item": project})
}
