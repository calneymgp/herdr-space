package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	githubsvc "herdr-space/internal/github"
	"herdr-space/internal/model"
)

// TestPerfGitDiscovery is opt-in so ordinary test runs never launch a benchmark.
// The shell shim deliberately replaces git only inside this test process.
func TestPerfGitDiscovery(t *testing.T) {
	if os.Getenv("HERDR_PERF_RUN") != "1" {
		t.Skip("opt-in performance measurement")
	}
	for _, scenario := range []struct{ spaces, samples int }{{20, 20}, {100, 5}} {
		t.Run(fmt.Sprintf("spaces_%d", scenario.spaces), func(t *testing.T) {
			root := t.TempDir()
			bin := t.TempDir()
			shim := "#!/bin/sh\nprintf '%s\\t%s\\n' \"$3\" \"$2\" >> \"$PERF_GIT_CALLS\"\nsleep 0.02\ncase \"$3\" in\n rev-parse) printf '%s\\n' \"$2\" ;;\n config) cat \"$2/remote.fixture\" ;;\n *) exit 1 ;;\nesac\n"
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			callsPath := filepath.Join(root, "git-calls.log")
			t.Setenv("PERF_GIT_CALLS", callsPath)
			inv := model.Inventory{}
			for i := 0; i < scenario.spaces; i++ {
				p := filepath.Join(root, "repo-"+strconv.Itoa(i))
				if err := os.Mkdir(p, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(p, "remote.fixture"), []byte("https://github.com/fixture/repo-"+strconv.Itoa(i)+".git\n"), 0600); err != nil {
					t.Fatal(err)
				}
				inv.Spaces = append(inv.Spaces, model.Space{ID: "s:w:" + strconv.Itoa(i), Name: "Space", ServerID: "s", WorkspaceID: "w", Path: p})
			}
			s, token, _ := newGitHubAPITest(t, []string{root}, inv, &githubTestProvider{status: githubsvc.Status{Available: true, Authenticated: true}})
			for i := -1; i < scenario.samples; i++ {
				if err := os.WriteFile(callsPath, nil, 0600); err != nil {
					t.Fatal(err)
				}
				start := time.Now()
				rr := githubRequest(t, s, http.MethodGet, "/api/v1/github/repositories", "", token, "")
				elapsed := time.Since(start)
				if rr.Code != 200 {
					t.Fatalf("status=%d", rr.Code)
				}
				if count := strings.Count(rr.Body.String(), "\"space_id\""); count != scenario.spaces {
					t.Fatalf("items=%d want=%d", count, scenario.spaces)
				}
				data, err := os.ReadFile(callsPath)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				observed := make(map[string]int, len(lines))
				for _, line := range lines {
					observed[line]++
				}
				if len(lines) != scenario.spaces*2 {
					t.Fatalf("sample %d: git calls=%d want=%d", i, len(lines), scenario.spaces*2)
				}
				for spaceIndex, space := range inv.Spaces {
					for _, method := range []string{"rev-parse", "config"} {
						if got := observed[method+"\t"+space.Path]; got != 1 {
							t.Fatalf("sample %d space %d %s calls=%d want=1", i, spaceIndex, method, got)
						}
					}
				}
				revParseCalls, configCalls := 0, 0
				for line, count := range observed {
					if strings.HasPrefix(line, "rev-parse\t") {
						revParseCalls += count
					}
					if strings.HasPrefix(line, "config\t") {
						configCalls += count
					}
				}
				if i >= 0 {
					t.Logf("PERF kind=github spaces=%d sample=%d elapsed_ns=%d git_calls=%d rev_parse_calls=%d config_calls=%d delay_ms=20", scenario.spaces, i, elapsed.Nanoseconds(), len(lines), revParseCalls, configCalls)
				}
			}
		})
	}
}
