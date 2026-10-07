package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"herdr-space/internal/auth"
	githubsvc "herdr-space/internal/github"
	"herdr-space/internal/model"
)

type githubTestProvider struct {
	status        githubsvc.Status
	page          githubsvc.IssuePage
	listRepo      string
	listState     githubsvc.State
	listPage      int
	createdRepo   string
	createdTitle  string
	createdBody   string
	updatedRepo   string
	updatedNumber int
	update        githubsvc.IssueUpdate
	createCalls   int
	updateCalls   int
}

func (p *githubTestProvider) Status(context.Context) githubsvc.Status { return p.status }
func (p *githubTestProvider) ListIssues(_ context.Context, repository string, state githubsvc.State, page int) (githubsvc.IssuePage, error) {
	p.listRepo, p.listState, p.listPage = repository, state, page
	return p.page, nil
}
func (p *githubTestProvider) CreateIssue(_ context.Context, repository, title, body string) (githubsvc.Issue, error) {
	p.createCalls++
	p.createdRepo, p.createdTitle, p.createdBody = repository, title, body
	return githubsvc.Issue{Number: 41, Title: title, Body: body, State: "open", UpdatedAt: "2026-10-06T12:00:00Z"}, nil
}

func (p *githubTestProvider) UpdateIssue(_ context.Context, repository string, number int, update githubsvc.IssueUpdate) (githubsvc.Issue, error) {
	p.updateCalls++
	p.updatedRepo, p.updatedNumber, p.update = repository, number, update
	issue := githubsvc.Issue{Number: number, State: githubsvc.StateOpen, UpdatedAt: "2026-10-06T12:00:00Z"}
	if update.Title != nil {
		issue.Title = *update.Title
	}
	if update.Body != nil {
		issue.Body = *update.Body
	}
	if update.State != nil {
		issue.State = *update.State
	}
	return issue, nil
}

type githubSpaceProvider struct {
	fakeProvider
	inventory model.Inventory
}

func (p githubSpaceProvider) Inventory(context.Context) (model.Inventory, error) {
	return p.inventory, nil
}

func newGitHubAPITest(t *testing.T, roots []string, inventory model.Inventory, gh githubsvc.Provider) (*Server, string, string) {
	t.Helper()
	s, err := New(Config{DataDir: t.TempDir(), Origin: "https://example.test", ProjectRoots: roots, GitHub: gh}, nil, githubSpaceProvider{inventory: inventory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	secret, _, err := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, err := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, time.Now()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s, token, csrf
}

func gitRepository(t *testing.T, root, name, remote string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-C", path, "init", "-q"}, {"-C", path, "remote", "add", "origin", remote}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git setup failed: %v (%s)", err, out)
		}
	}
	return path
}

func githubRequest(t *testing.T, s *Server, method, path, body, token, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	if method != http.MethodGet {
		req.Header.Set("Origin", "https://example.test")
		req.Header.Set("X-CSRF-Token", csrf)
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	return rr
}

func TestGitHubRepositoriesAreDerivedOnlyFromCurrentAllowedSpaces(t *testing.T) {
	root := t.TempDir()
	githubPath := gitRepository(t, root, "github", "https://github.com/herdr-fixture/workspace.git")
	otherPath := gitRepository(t, root, "other", "https://example.test/private/repo.git")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}}
	inventory := model.Inventory{Spaces: []model.Space{
		{ID: "server:workspace:github", Name: "GitHub Space", Path: githubPath, ServerID: "server", WorkspaceID: "github"},
		{ID: "server:workspace:other", Name: "Other Space", Path: otherPath, ServerID: "server", WorkspaceID: "other"},
	}}
	s, token, _ := newGitHubAPITest(t, []string{root}, inventory, gh)
	rr := githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Items []struct {
			SpaceID    string `json:"space_id"`
			SpaceName  string `json:"space_name"`
			Repository string `json:"repository"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].SpaceID != "server:workspace:github" || got.Items[0].Repository != "herdr-fixture/workspace" {
		t.Fatalf("unexpected repository list: %+v", got.Items)
	}
}

func fakeGitDiscovery(t *testing.T, count int, delay string) (string, model.Inventory, string) {
	t.Helper()
	root := t.TempDir()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls")
	shim := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$GIT_FAKE_CALLS\"\n" +
		"printf 'start\\n' >> \"$GIT_FAKE_EVENTS\"\n" +
		"trap 'printf \"end\\n\" >> \"$GIT_FAKE_EVENTS\"' EXIT\n" +
		"sleep \"$GIT_FAKE_DELAY\"\n" +
		"case \"$3\" in\n" +
		"rev-parse) printf '%s\\n' \"$2\" ;;\n" +
		"config) cat \"$2/remote.fixture\" ;;\n" +
		"*) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_FAKE_CALLS", log)
	t.Setenv("GIT_FAKE_EVENTS", log+".events")
	t.Setenv("GIT_FAKE_DELAY", delay)
	inventory := model.Inventory{}
	for i := 0; i < count; i++ {
		path := filepath.Join(root, "repo-"+strconv.Itoa(i))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "remote.fixture"), []byte("https://github.com/fixture/repo-"+strconv.Itoa(i)+".git\n"), 0600); err != nil {
			t.Fatal(err)
		}
		inventory.Spaces = append(inventory.Spaces, model.Space{ID: "s:w:" + strconv.Itoa(i), Name: "Space " + strconv.Itoa(i), ServerID: "s", WorkspaceID: "w", Path: path})
	}
	return root, inventory, log
}

func fakeGitCalls(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func TestGitHubDiscoveryHundredSpacesBoundedAndOrdered(t *testing.T) {
	root, inventory, log := fakeGitDiscovery(t, 100, "0.02")
	s, token, _ := newGitHubAPITest(t, []string{root}, inventory, &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}})
	start := time.Now()
	rr := githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
	elapsed := time.Since(start)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var got struct {
		Items []struct {
			SpaceID    string `json:"space_id"`
			Repository string `json:"repository"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 100 || fakeGitCalls(t, log) != 200 {
		t.Fatalf("items=%d calls=%d", len(got.Items), fakeGitCalls(t, log))
	}
	for i, item := range got.Items {
		if item.SpaceID != "s:w:"+strconv.Itoa(i) || item.Repository != "fixture/repo-"+strconv.Itoa(i) {
			t.Fatalf("index %d: %+v", i, item)
		}
	}
	if elapsed > 3500*time.Millisecond {
		t.Fatalf("discovery took %s for 100 spaces", elapsed)
	}
	t.Logf("100 spaces: %d Git calls, discovery=%s", fakeGitCalls(t, log), elapsed)
}

func TestGitHubDiscoveryCancellationStopsDispatch(t *testing.T) {
	root, inventory, log := fakeGitDiscovery(t, 100, "0.2")
	s, token, _ := newGitHubAPITest(t, []string{root}, inventory, &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/github/repositories", nil)
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	ctx, cancel := context.WithTimeout(req.Context(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req.WithContext(ctx))
	elapsed := time.Since(start)
	if rr.Code != http.StatusServiceUnavailable || elapsed > time.Second {
		t.Fatalf("status=%d duration=%s", rr.Code, elapsed)
	}
	if calls := fakeGitCalls(t, log); calls > gitDiscoveryWorkers {
		t.Fatalf("dispatched %d subprocesses after cancellation", calls)
	}
	t.Logf("cancelled after %s, Git calls=%d", elapsed, fakeGitCalls(t, log))
}

func TestGitHubDiscoveryLimitsProcessesAcrossRequests(t *testing.T) {
	root, inventory, log := fakeGitDiscovery(t, 24, "0.04")
	s, token, _ := newGitHubAPITest(t, []string{root}, inventory, &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}})
	var results [2]*httptest.ResponseRecorder
	var group sync.WaitGroup
	for i := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			results[i] = githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
		}()
	}
	group.Wait()
	for i, result := range results {
		if result.Code != http.StatusOK {
			t.Fatalf("request %d: status=%d", i, result.Code)
		}
	}
	if calls := fakeGitCalls(t, log); calls != 96 {
		t.Fatalf("calls=%d, want 96", calls)
	}
	events, err := os.ReadFile(log + ".events")
	if err != nil {
		t.Fatal(err)
	}
	active, peak := 0, 0
	for _, event := range strings.Fields(string(events)) {
		switch event {
		case "start":
			active++
			peak = max(peak, active)
		case "end":
			active--
		default:
			t.Fatalf("unexpected event %q", event)
		}
		if active < 0 || peak > gitDiscoveryWorkers {
			t.Fatalf("active=%d peak=%d limit=%d", active, peak, gitDiscoveryWorkers)
		}
	}
	if active != 0 {
		t.Fatalf("unfinished subprocesses=%d", active)
	}
	t.Logf("two requests: Git calls=%d, peak processes=%d", fakeGitCalls(t, log), peak)
}

func TestGitHubDiscoveryRejectsUnauthenticatedRequestAndRefreshesAssociations(t *testing.T) {
	root, inventory, log := fakeGitDiscovery(t, 2, "0")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}}
	s, token, csrf := newGitHubAPITest(t, []string{root}, inventory, gh)
	list := func() *httptest.ResponseRecorder {
		return githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
	}
	if rr := list(); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "fixture/repo-0") {
		t.Fatalf("initial list status=%d body=%s", rr.Code, rr.Body.String())
	}
	initialCalls := fakeGitCalls(t, log)
	unauthenticated := httptest.NewRecorder()
	s.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v1/github/repositories", nil))
	if unauthenticated.Code != http.StatusUnauthorized || fakeGitCalls(t, log) != initialCalls {
		t.Fatalf("unauthenticated status=%d calls=%d", unauthenticated.Code, fakeGitCalls(t, log))
	}
	if err := os.WriteFile(filepath.Join(inventory.Spaces[0].Path, "remote.fixture"), []byte("https://github.com/fixture/changed.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// CLI auth status does not cache or authorize this local association.
	gh.status.Authenticated = false
	if rr := list(); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "fixture/changed") || strings.Contains(rr.Body.String(), "fixture/repo-0") {
		t.Fatalf("changed remote status=%d body=%s", rr.Code, rr.Body.String())
	}
	gh.status.Authenticated = true
	create := githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"s:w:0","title":"New"}`, token, csrf)
	if create.Code != http.StatusCreated || gh.createdRepo != "fixture/changed" {
		t.Fatalf("create after remote change status=%d repo=%q", create.Code, gh.createdRepo)
	}
	if err := os.WriteFile(filepath.Join(inventory.Spaces[0].Path, "remote.fixture"), []byte("https://example.test/foreign/repo.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if rr := list(); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "s:w:0") {
		t.Fatalf("disallowed remote status=%d body=%s", rr.Code, rr.Body.String())
	}
	update := githubRequest(t, s, http.MethodPatch, "/api/v1/github/issues/41", `{"space_id":"s:w:0","state":"closed"}`, token, csrf)
	if update.Code != http.StatusServiceUnavailable || gh.updateCalls != 0 {
		t.Fatalf("update after disallowed remote status=%d calls=%d", update.Code, gh.updateCalls)
	}
	// A removed Space cannot retain its previous association or authorize a write.
	s.Provider = githubSpaceProvider{inventory: model.Inventory{Spaces: inventory.Spaces[1:]}}
	if rr := list(); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "s:w:0") {
		t.Fatalf("removed space status=%d body=%s", rr.Code, rr.Body.String())
	}
	create = githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"s:w:0","title":"New"}`, token, csrf)
	if create.Code != http.StatusNotFound || gh.createCalls != 1 {
		t.Fatalf("removed space create status=%d calls=%d", create.Code, gh.createCalls)
	}
}

func TestGitHubDiscoveryFollowsCurrentPathAndFailsClosed(t *testing.T) {
	root, inventory, _ := fakeGitDiscovery(t, 2, "0")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}}
	s, token, csrf := newGitHubAPITest(t, []string{root}, inventory, gh)
	list := func() *httptest.ResponseRecorder {
		return githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
	}
	space := inventory.Spaces[0]
	space.Path = inventory.Spaces[1].Path
	s.Provider = githubSpaceProvider{inventory: model.Inventory{Spaces: []model.Space{space}}}
	if rr := list(); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "fixture/repo-1") || strings.Contains(rr.Body.String(), "fixture/repo-0") {
		t.Fatalf("changed path status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := os.Remove(filepath.Join(space.Path, "remote.fixture")); err != nil {
		t.Fatal(err)
	}
	if rr := list(); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "s:w:0") {
		t.Fatalf("failed remote status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"s:w:0","title":"New"}`, token, csrf); rr.Code != http.StatusServiceUnavailable || gh.createCalls != 0 {
		t.Fatalf("failed remote create status=%d calls=%d", rr.Code, gh.createCalls)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	space.Path = link
	s.Provider = githubSpaceProvider{inventory: model.Inventory{Spaces: []model.Space{space}}}
	if rr := list(); rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "s:w:0") {
		t.Fatalf("outside symlink status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr := githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"s:w:0","title":"New"}`, token, csrf); rr.Code != http.StatusServiceUnavailable || gh.createCalls != 0 {
		t.Fatalf("outside symlink create status=%d calls=%d", rr.Code, gh.createCalls)
	}
	if rr := githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"s:w:0","title":"New"}`, token, "wrong"); rr.Code != http.StatusForbidden || gh.createCalls != 0 {
		t.Fatalf("invalid CSRF status=%d calls=%d", rr.Code, gh.createCalls)
	}
}

func TestGitHubIssueListResolvesSpaceAndReturnsPage(t *testing.T) {
	root := t.TempDir()
	path := gitRepository(t, root, "workspace", "https://github.com/herdr-fixture/workspace.git")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}, page: githubsvc.IssuePage{Items: []githubsvc.Issue{{Number: 17, Title: "Issue", State: "closed", URL: "https://github.com/herdr-fixture/workspace/issues/17"}}, Page: 2, HasMore: true}}
	spaceID := "server:workspace:one"
	s, token, _ := newGitHubAPITest(t, []string{root}, model.Inventory{Spaces: []model.Space{{ID: spaceID, Name: "Workspace", Path: path, ServerID: "server", WorkspaceID: "one"}}}, gh)
	pathQ := "/api/v1/github/issues?" + url.Values{"space_id": {spaceID}, "state": {"closed"}, "page": {"2"}}.Encode()
	rr := githubRequest(t, s, http.MethodGet, pathQ, "", token, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if gh.listRepo != "herdr-fixture/workspace" || gh.listState != "closed" || gh.listPage != 2 {
		t.Fatalf("unexpected provider request: repo=%q state=%q page=%d", gh.listRepo, gh.listState, gh.listPage)
	}
	var got githubsvc.IssuePage
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Page != 2 || !got.HasMore || len(got.Items) != 1 {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestGitHubIssueMutationsUseSpaceRepositoryAndValidateState(t *testing.T) {
	root := t.TempDir()
	path := gitRepository(t, root, "workspace", "https://github.com/herdr-fixture/workspace.git")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}}
	spaceID := "server:workspace:one"
	s, token, csrf := newGitHubAPITest(t, []string{root}, model.Inventory{Spaces: []model.Space{{ID: spaceID, Name: "Workspace", Path: path, ServerID: "server", WorkspaceID: "one"}}}, gh)
	create := githubRequest(t, s, http.MethodPost, "/api/v1/github/issues", `{"space_id":"`+spaceID+`","title":"New","body":"details"}`, token, csrf)
	if create.Code != http.StatusCreated || gh.createCalls != 1 || gh.createdRepo != "herdr-fixture/workspace" || gh.createdTitle != "New" || gh.createdBody != "details" {
		t.Fatalf("create status=%d calls=%d repo=%q", create.Code, gh.createCalls, gh.createdRepo)
	}
	update := githubRequest(t, s, http.MethodPatch, "/api/v1/github/issues/41", `{"space_id":"`+spaceID+`","state":"closed"}`, token, csrf)
	if update.Code != http.StatusOK || gh.updateCalls != 1 || gh.updatedRepo != "herdr-fixture/workspace" || gh.updatedNumber != 41 || gh.update.State == nil || *gh.update.State != "closed" || gh.update.Title != nil || gh.update.Body != nil {
		t.Fatalf("state update status=%d calls=%d update=%+v", update.Code, gh.updateCalls, gh.update)
	}
	edited := githubRequest(t, s, http.MethodPatch, "/api/v1/github/issues/41", `{"space_id":"`+spaceID+`","title":"Updated","body":"more"}`, token, csrf)
	if edited.Code != http.StatusOK || gh.updateCalls != 2 || gh.update.Title == nil || *gh.update.Title != "Updated" || gh.update.Body == nil || *gh.update.Body != "more" || gh.update.State != nil {
		t.Fatalf("edit status=%d calls=%d update=%+v", edited.Code, gh.updateCalls, gh.update)
	}
	invalid := githubRequest(t, s, http.MethodPatch, "/api/v1/github/issues/41", `{"space_id":"`+spaceID+`","state":"done"}`, token, csrf)
	if invalid.Code != http.StatusBadRequest || gh.updateCalls != 2 {
		t.Fatalf("invalid state status=%d provider calls=%d", invalid.Code, gh.updateCalls)
	}
}

func TestGitHubIssuesRejectStaleSpaceInventory(t *testing.T) {
	root := t.TempDir()
	path := gitRepository(t, root, "workspace", "https://github.com/herdr-fixture/workspace.git")
	gh := &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}}
	s, token, _ := newGitHubAPITest(t, []string{root}, model.Inventory{Stale: true, Spaces: []model.Space{{ID: "server:workspace:one", Name: "Workspace", Path: path, ServerID: "server", WorkspaceID: "one"}}}, gh)
	pathQ := "/api/v1/github/issues?space_id=server%3Aworkspace%3Aone&state=open&page=1"
	rr := githubRequest(t, s, http.MethodGet, pathQ, "", token, "")
	if rr.Code != http.StatusServiceUnavailable || gh.listRepo != "" {
		t.Fatalf("status=%d provider repo=%q", rr.Code, gh.listRepo)
	}
}

func TestSpaceProjectResolutionIsRootBoundAndIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workspace")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	spaceID := "server:workspace:one"
	s, token, csrf := newGitHubAPITest(t, []string{root}, model.Inventory{Spaces: []model.Space{{ID: spaceID, Name: "Workspace", Path: path, ServerID: "server", WorkspaceID: "one"}}}, &githubTestProvider{})
	endpoint := "/api/v1/spaces/" + url.PathEscape(spaceID) + "/project"
	first := githubRequest(t, s, http.MethodPost, endpoint, `{}`, token, csrf)
	second := githubRequest(t, s, http.MethodPost, endpoint, `{}`, token, csrf)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("statuses=%d,%d bodies=%s %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	var a, b struct {
		Item struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"item"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	if a.Item.ID == "" || a.Item.ID != b.Item.ID || a.Item.Path != path || b.Item.Path != path {
		t.Fatalf("project resolution not idempotent: %+v %+v", a.Item, b.Item)
	}
	projects, err := s.Store.ListProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects=%d error=%v", len(projects), err)
	}
}
