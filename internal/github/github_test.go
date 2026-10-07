package github

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRepositoryFromRemoteOnlyAcceptsGitHubOwnerRepository(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
		ok     bool
	}{
		{name: "https credentials are removed", remote: "https://user:secret@github.com/Owner/repo.git", want: "Owner/repo", ok: true},
		{name: "ssh scp form", remote: "git@github.com:Owner/repo.git", want: "Owner/repo", ok: true},
		{name: "ssh url form", remote: "ssh://git@github.com/Owner/repo.git", want: "Owner/repo", ok: true},
		{name: "other host rejected", remote: "https://github.example/Owner/repo.git", ok: false},
		{name: "github suffix rejected", remote: "https://github.com.evil.test/Owner/repo.git", ok: false},
		{name: "extra path rejected", remote: "https://github.com/Owner/repo/tree/main", ok: false},
		{name: "local path rejected", remote: "../repo", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RepositoryFromRemote(tt.remote)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("RepositoryFromRemote() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestCLIListIssuesKeepsRawPageBoundaryWithoutSkippingIssue51(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "endpoints")
	binary := filepath.Join(dir, "gh")
	var script strings.Builder
	script.WriteString("#!/bin/sh\nendpoint=\"\"\nfor arg do endpoint=\"$arg\"; done\nprintf '%s\\n' \"$endpoint\" >> ")
	script.WriteString(shellQuote(capture))
	script.WriteString("\ncase \"$endpoint\" in\n  *page=1)\n    printf '['\n    i=1\n    while [ \"$i\" -le 50 ]; do\n      [ \"$i\" -gt 1 ] && printf ','\n      printf '{\"number\":%s,\"title\":\"Issue %s\",\"body\":\"\",\"state\":\"open\",\"html_url\":\"https://github.com/herdr-fixture/workspace/issues/%s\",\"updated_at\":\"2026-10-06T12:00:00Z\"}' \"$i\" \"$i\" \"$i\"\n      i=$((i + 1))\n    done\n    printf ']'\n    ;;\n  *page=2)\n    printf '[{\"number\":51,\"title\":\"Issue 51\",\"body\":\"\",\"state\":\"open\",\"html_url\":\"https://github.com/herdr-fixture/workspace/issues/51\",\"updated_at\":\"2026-10-06T12:00:00Z\"}]'\n    ;;\nesac\n")
	if err := os.WriteFile(binary, []byte(script.String()), 0700); err != nil {
		t.Fatal(err)
	}
	cli := NewCLIAt(binary)
	first, err := cli.ListIssues(context.Background(), "herdr-fixture/workspace", StateOpen, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cli.ListIssues(context.Background(), "herdr-fixture/workspace", StateOpen, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 50 || first.Items[49].Number != 50 || !first.HasMore {
		t.Fatalf("unexpected first page: count=%d last=%d has_more=%v", len(first.Items), first.Items[len(first.Items)-1].Number, first.HasMore)
	}
	if len(second.Items) != 1 || second.Items[0].Number != 51 || second.HasMore {
		t.Fatalf("issue at page boundary was skipped: %+v", second)
	}
	endpoints, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := "repos/herdr-fixture/workspace/issues?state=open&per_page=50&page=1\nrepos/herdr-fixture/workspace/issues?state=open&per_page=50&page=2\n"
	if string(endpoints) != want {
		t.Fatalf("unexpected GitHub CLI endpoints: %q", endpoints)
	}
}

func TestCLICommandTimeoutBoundsInheritedPipe(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	binary := filepath.Join(dir, "gh")
	script := "#!/bin/sh\nsleep 3 &\nprintf '%s' \"$!\" > " + shellQuote(pidFile) + "\nwait\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := CLI{Binary: binary, Timeout: 100 * time.Millisecond}
	started := time.Now()
	_, err := cli.ListIssues(context.Background(), "herdr-fixture/workspace", StateOpen, 1)
	if err == nil {
		t.Fatal("timed-out CLI request succeeded")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("CLI timeout waited for inherited pipe: %s", elapsed)
	}
	if pidBytes, readErr := os.ReadFile(pidFile); readErr == nil {
		if pid, parseErr := strconv.Atoi(string(pidBytes)); parseErr == nil && pid > 0 {
			if process, findErr := os.FindProcess(pid); findErr == nil {
				_ = process.Signal(syscall.SIGKILL)
			}
		}
	}
}
