// Read-only compatibility probe: prints aggregated metadata, never session
// names, directories, IDs, process arguments or conversation content.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"herdr-space/internal/model"
	"herdr-space/internal/runtime"
	"os"
	"sort"
	"time"
)

func main() {
	observe := flag.Bool("observe", false, "read-only live stream compatibility check; no terminal content is printed")
	identityDigest := flag.Bool("identity-digest", false, "compare live identities across app restarts without printing IDs or PIDs")
	flag.Parse()
	m, err := runtime.New(runtime.Config{})
	if err != nil {
		panic(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	inv, err := m.Inventory(ctx)
	if err != nil {
		panic(err)
	}
	counts := map[string]int{}
	for _, s := range inv.Items {
		counts[s.Source+":"+s.Membership]++
	}
	result := map[string]any{"stale": inv.Stale, "counts": counts, "warning_present": inv.Warning != ""}
	if *identityDigest {
		identities := make([]string, 0, len(inv.Items))
		for _, session := range inv.Items {
			identities = append(identities, fmt.Sprintf("%s|%s|%d|%s", session.Source, session.ID, session.PID, session.StartTime))
		}
		sort.Strings(identities)
		data, _ := json.Marshal(identities)
		digest := sha256.Sum256(data)
		result["identity_digest"] = hex.EncodeToString(digest[:])
	}
	if *observe {
		for _, session := range inv.Items {
			if !session.Alive || session.Source != "herdr" {
				continue
			}
			canObserve := false
			for _, capability := range session.Capabilities {
				canObserve = canObserve || capability == "observe"
			}
			if !canObserve {
				continue
			}
			stream, attachErr := m.Attach(ctx, model.StreamRequest{ID: session.ID, Mode: "observe", Cols: 100, Rows: 30})
			if attachErr != nil {
				panic("read-only stream attach failed")
			}
			select {
			case frame, ok := <-stream.Frames():
				result["live_frame_received"] = ok && len(frame) > 0
			case <-time.After(5 * time.Second):
				result["live_frame_received"] = false
			}
			result["bridge_released"] = stream.Close() == nil
			break
		}
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
