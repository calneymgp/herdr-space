package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"
)

func (c Client) Subscribe(ctx context.Context, onEvent func()) error {
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, err := (&net.Dialer{}).DialContext(dctx, "unix", c.Socket)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	go func() { <-watchCtx.Done(); _ = conn.Close() }()
	req := map[string]any{"id": "1", "method": "events.subscribe", "params": map[string]any{"subscriptions": []map[string]string{{"type": "pane.created"}, {"type": "pane.closed"}, {"type": "pane.exited"}, {"type": "pane.updated"}, {"type": "pane.agent_detected"}, {"type": "workspace.closed"}}}}
	if err = json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), MaxResponseBytes)
	first := true
	for scanner.Scan() {
		var msg struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Event  json.RawMessage `json:"event"`
		}
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			return errors.New("invalid HERDR event")
		}
		if first {
			first = false
			if msg.ID != "1" || (len(msg.Error) > 0 && string(msg.Error) != "null") || len(msg.Result) == 0 {
				return errors.New("HERDR subscription rejected")
			}
			_ = conn.SetDeadline(time.Time{})
			continue
		}
		if len(msg.Event) > 0 {
			onEvent()
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("HERDR subscription ended")
}
