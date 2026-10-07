package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout = 15 * time.Second
	maxStdout      = 8 << 20
	maxStderr      = 128 << 10
	issuePageSize  = 50
)

var errOutputLimit = errors.New("command output limit exceeded")

type CLI struct {
	Binary  string
	Timeout time.Duration
}

func NewCLI() CLI { return CLI{} }

// NewCLIAt is useful for controlled tests and installations with a fixed gh
// path. Production normally resolves gh through the service's PATH.
func NewCLIAt(binary string) CLI { return CLI{Binary: binary} }

func (c CLI) timeout() time.Duration {
	if c.Timeout <= 0 || c.Timeout > 60*time.Second {
		return defaultTimeout
	}
	return c.Timeout
}

func (c CLI) commandPath() (string, error) {
	binary := c.Binary
	if binary == "" {
		binary = "gh"
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return "", errors.New("GitHub CLI unavailable")
	}
	return resolved, nil
}

type boundedOutput struct {
	bytes.Buffer
	limit   int
	cancel  context.CancelFunc
	tooLong bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		b.tooLong = true
		b.cancel()
		return 0, errOutputLimit
	}
	return b.Buffer.Write(p)
}

func (c CLI) run(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	path, err := c.commandPath()
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()
	cmd := exec.CommandContext(commandCtx, path, args...)
	cmd.WaitDelay = 500 * time.Millisecond
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	stdout := &boundedOutput{limit: maxStdout, cancel: cancel}
	stderr := &boundedOutput{limit: maxStderr, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil || stdout.tooLong || stderr.tooLong || commandCtx.Err() != nil {
		return nil, errors.New("GitHub CLI request failed")
	}
	return append([]byte(nil), stdout.Bytes()...), nil
}

func (c CLI) Status(ctx context.Context) Status {
	if _, err := c.commandPath(); err != nil {
		return Status{}
	}
	_, err := c.run(ctx, nil, "auth", "status", "--hostname", "github.com")
	return Status{Available: true, Authenticated: err == nil}
}

func (c CLI) ListIssues(ctx context.Context, repository string, state State, page int) (IssuePage, error) {
	if !validRepository(repository) || (state != StateOpen && state != StateClosed && state != StateAll) || page < 1 || page > 10000 {
		return IssuePage{}, ErrInvalid
	}
	endpoint := fmt.Sprintf("repos/%s/issues?state=%s&per_page=%d&page=%d", repository, state, issuePageSize, page)
	out, err := c.run(ctx, nil, "api", "--hostname", "github.com", endpoint)
	if err != nil {
		return IssuePage{}, err
	}
	var raw []struct {
		Number      int             `json:"number"`
		Title       string          `json:"title"`
		Body        string          `json:"body"`
		State       State           `json:"state"`
		URL         string          `json:"html_url"`
		UpdatedAt   string          `json:"updated_at"`
		PullRequest json.RawMessage `json:"pull_request"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return IssuePage{}, errors.New("GitHub response invalid")
	}
	result := IssuePage{Items: []Issue{}, Page: page, HasMore: len(raw) >= issuePageSize}
	if len(raw) > issuePageSize {
		raw = raw[:issuePageSize]
	}
	for _, item := range raw {
		if len(item.PullRequest) > 0 && string(item.PullRequest) != "null" {
			continue
		}
		if item.Number <= 0 || (item.State != StateOpen && item.State != StateClosed) {
			continue
		}
		result.Items = append(result.Items, Issue{Number: item.Number, Title: item.Title, Body: item.Body, State: item.State, URL: item.URL, UpdatedAt: item.UpdatedAt})
	}
	return result, nil
}

func validIssue(title, body string) bool {
	return strings.TrimSpace(title) != "" && len(title) <= 256 && len(body) <= 65536
}

func (c CLI) CreateIssue(ctx context.Context, repository, title, body string) (Issue, error) {
	if !validRepository(repository) || !validIssue(title, body) {
		return Issue{}, ErrInvalid
	}
	input, err := json.Marshal(struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}{Title: title, Body: body})
	if err != nil {
		return Issue{}, ErrInvalid
	}
	return c.writeIssue(ctx, repository, 0, input)
}

func (c CLI) UpdateIssue(ctx context.Context, repository string, number int, update IssueUpdate) (Issue, error) {
	if !validRepository(repository) || number < 1 || update.Title == nil && update.Body == nil && update.State == nil {
		return Issue{}, ErrInvalid
	}
	fields := map[string]any{}
	if update.Title != nil {
		if strings.TrimSpace(*update.Title) == "" || len(*update.Title) > 256 {
			return Issue{}, ErrInvalid
		}
		fields["title"] = *update.Title
	}
	if update.Body != nil {
		if len(*update.Body) > 65536 {
			return Issue{}, ErrInvalid
		}
		fields["body"] = *update.Body
	}
	if update.State != nil {
		if *update.State != StateOpen && *update.State != StateClosed {
			return Issue{}, ErrInvalid
		}
		fields["state"] = *update.State
	}
	input, err := json.Marshal(fields)
	if err != nil {
		return Issue{}, ErrInvalid
	}
	return c.writeIssue(ctx, repository, number, input)
}

func (c CLI) writeIssue(ctx context.Context, repository string, number int, input []byte) (Issue, error) {
	endpoint := "repos/" + repository + "/issues"
	method := "POST"
	if number > 0 {
		endpoint += "/" + strconv.Itoa(number)
		method = "PATCH"
	}
	out, err := c.run(ctx, input, "api", "--hostname", "github.com", "--method", method, endpoint, "--input", "-")
	if err != nil {
		return Issue{}, err
	}
	var raw struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		State     State  `json:"state"`
		URL       string `json:"html_url"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(out, &raw); err != nil || raw.Number <= 0 || (raw.State != StateOpen && raw.State != StateClosed) {
		return Issue{}, errors.New("GitHub response invalid")
	}
	return Issue{Number: raw.Number, Title: raw.Title, Body: raw.Body, State: raw.State, URL: raw.URL, UpdatedAt: raw.UpdatedAt}, nil
}

var _ Provider = CLI{}

var _ io.Writer = (*boundedOutput)(nil)
