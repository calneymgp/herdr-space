package runtime

import (
	"herdr-space/internal/model"
	"reflect"
	"testing"
)

func TestResumeCommandsUseOfficialShapes(t *testing.T) {
	cases := []struct {
		agent, ref string
		want       []string
	}{{"codex", "abc", []string{"resume", "abc"}}, {"claude", "abc", []string{"--resume", "abc"}}, {"pi", "/tmp/session.jsonl", []string{"--session", "/tmp/session.jsonl"}}}
	for _, tc := range cases {
		got, e := resumeArgs(tc.agent, tc.ref)
		if e != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s args=%v err=%v", tc.agent, got, e)
		}
	}
	if _, e := resumeArgs("opencode", "abc"); e == nil {
		t.Fatal("unsupported OpenCode resume accepted")
	}
}
func TestResumeRecordInvalidatedByNewProcessOnSameTerminal(t *testing.T) {
	old := entry{session: model.Session{PID: 10, StartTime: "100"}}
	same := entry{session: model.Session{PID: 10, StartTime: "100", Alive: true}}
	newer := entry{session: model.Session{PID: 10, StartTime: "200", Alive: true}}
	if !keepStopped(old, same) || keepStopped(old, newer) {
		t.Fatal("stopped record lifecycle mismatch")
	}
}
func TestConsumedReferenceClearsOnlyForProvenNewConversation(t *testing.T) {
	old := entry{session: model.Session{PID: 77, StartTime: "123", Alive: false}, agentRef: "old"}
	newer := entry{session: model.Session{PID: 101, StartTime: "777", Alive: true}, agentRef: "new"}
	sameRef := entry{session: model.Session{PID: 101, StartTime: "777", Alive: true}, agentRef: "old"}
	if !newConversation(old, newer) || newConversation(old, sameRef) {
		t.Fatal("consumption generation rule incorrect")
	}
}
