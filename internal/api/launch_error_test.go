package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"herdr-space/internal/auth"
	"herdr-space/internal/model"
)

type failedLaunchProvider struct{ fakeProvider }

func (failedLaunchProvider) Launch(context.Context, model.StartRequest) (model.Session, error) {
	return model.Session{}, &model.LaunchError{WorkspaceID: "workspace-123", PublicMessage: "Could not start the agent in the created workspace.", Cause: errors.New("private runtime detail")}
}

func TestLaunchFailureReturnsCreatedWorkspaceWithoutPrivateCause(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, failedLaunchProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	project, e := s.Store.CreateProject(context.Background(), model.Project{Name: "A", Path: "/tmp/project"})
	if e != nil {
		t.Fatal(e)
	}
	secret, _, e := s.Auth.Setup(context.Background(), "admin", "correct horse battery staple")
	if e != nil {
		t.Fatal(e)
	}
	token, csrf, e := s.Auth.Login(context.Background(), "admin", "correct horse battery staple", auth.Code(secret, time.Now()), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	body, _ := json.Marshal(map[string]string{"agent": "codex", "launcher": "codex", "project_id": project.ID})
	req := httptest.NewRequest("POST", "/api/v1/sessions/start", bytes.NewReader(body))
	req.Header.Set("Origin", "https://example.test")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "herdr_session", Value: token})
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 503 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Error       string `json:"error"`
		WorkspaceID string `json:"workspace_id"`
	}
	if e := json.Unmarshal(rr.Body.Bytes(), &got); e != nil {
		t.Fatal(e)
	}
	if got.WorkspaceID != "workspace-123" || !bytes.Contains([]byte(got.Error), []byte("workspace-123")) || bytes.Contains(rr.Body.Bytes(), []byte("private runtime detail")) {
		t.Fatalf("unsafe response %+v", got)
	}
}
