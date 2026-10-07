// perfprobe performs one bounded, read-only inventory call. It never prints paths or identities.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"time"

	"herdr-space/internal/runtime"
)

func main() {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, e := runtime.New(runtime.Config{})
	if e != nil {
		os.Exit(1)
	}
	inv, e := m.Inventory(ctx)
	if e != nil {
		os.Exit(2)
	}
	ids := make([]string, 0, len(inv.Items))
	counts := map[string]int{}
	for _, v := range inv.Items {
		counts[v.Source]++
		ids = append(ids, v.ID)
	}
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"elapsed_ms": float64(time.Since(started).Microseconds()) / 1000, "timeout_ms": 5000, "items": len(inv.Items), "spaces": len(inv.Spaces), "counts": counts, "identity_digest": hex.EncodeToString(h.Sum(nil)), "stale": inv.Stale, "warning_present": inv.Warning != ""})
}
