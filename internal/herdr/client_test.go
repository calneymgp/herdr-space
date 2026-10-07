package herdr

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCallUsesOneBoundedNDJSONRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		d := json.NewDecoder(c)
		var req map[string]any
		if d.Decode(&req) != nil {
			return
		}
		if req["method"] != "session.snapshot" {
			return
		}
		c.Write([]byte(`{"id":"1","result":{"type":"session.snapshot","snapshot":{"panes":[]}}}` + "\n"))
	}()
	c := Client{Socket: path, Timeout: time.Second}
	var out struct {
		Snapshot struct {
			Panes []any `json:"panes"`
		} `json:"snapshot"`
	}
	if err := c.Call(context.Background(), "session.snapshot", map[string]any{}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Snapshot.Panes == nil {
		t.Fatal("missing snapshot")
	}
}
func TestCallRejectsOversizedResponse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		var req any
		json.NewDecoder(c).Decode(&req)
		c.Write([]byte(strings.Repeat("x", MaxResponseBytes+1) + "\n"))
	}()
	c := Client{Socket: path, Timeout: time.Second}
	var out any
	if err := c.Call(context.Background(), "session.snapshot", nil, &out); err == nil {
		t.Fatal("oversize accepted")
	}
}
