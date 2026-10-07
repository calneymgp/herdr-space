package main

import (
	"strings"
	"testing"
)

func TestServeRequiresExplicitOrigin(t *testing.T) {
	err := run([]string{"serve"})
	if err == nil || !strings.Contains(err.Error(), "--origin is required") {
		t.Fatalf("expected missing-origin error, got %v", err)
	}
}
