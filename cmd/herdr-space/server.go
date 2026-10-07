package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

func serveHTTP(ctx context.Context, server *http.Server, listener net.Listener, timeout time.Duration) error {
	defer listener.Close()
	serveDone := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-serveDone:
			if ctx.Err() == nil {
				shutdownDone <- nil
				return
			}
		}
		shutdown, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			_ = server.Close()
		}
		shutdownDone <- err
	}()
	err := server.Serve(listener)
	close(serveDone)
	if shutdownErr := <-shutdownDone; shutdownErr != nil {
		return shutdownErr
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
