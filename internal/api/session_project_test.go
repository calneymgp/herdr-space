package api

import (
	"context"
	"herdr-space/internal/model"
	"testing"
)

func TestMapsSessionToDeepestProject(t *testing.T) {
	s, e := New(Config{DataDir: t.TempDir(), Origin: "https://example.test"}, nil, fakeProvider{})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	root, e := s.Store.CreateProject(context.Background(), model.Project{Name: "root", Path: "/work"})
	if e != nil {
		t.Fatal(e)
	}
	child, e := s.Store.CreateProject(context.Background(), model.Project{Name: "child", Path: "/work/app"})
	if e != nil {
		t.Fatal(e)
	}
	inv := model.Inventory{Items: []model.Session{{ID: "one", CWD: "/work/app/sub"}, {ID: "two", CWD: "/work-other"}}}
	s.mapProjects(context.Background(), &inv)
	if inv.Items[0].ProjectID != child.ID || inv.Items[0].ProjectID == root.ID || inv.Items[1].ProjectID != "" {
		t.Fatalf("mapped %+v", inv.Items)
	}
}
