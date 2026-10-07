package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeHTTPWaitsForGracefulHandlerCompletion(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serveHTTP(ctx, server, listener, time.Second) }()
	requestDone := make(chan struct{})
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-served:
		t.Fatalf("returned before handler completed: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
	<-requestDone
}

func TestServeHTTPForcesCloseAfterShutdownTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	handlerExited := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; close(handlerExited) })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- serveHTTP(ctx, server, listener, 30*time.Millisecond) }()
	requestDone := make(chan struct{})
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case err := <-served:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown hung")
	}
	close(release)
	select {
	case <-handlerExited:
	case <-time.After(time.Second):
		t.Fatal("handler leaked")
	}
	<-requestDone
}

func TestServeHTTPReturnsWhenListenerAlreadyClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	result := make(chan error, 1)
	go func() { result <- serveHTTP(context.Background(), &http.Server{}, listener, time.Second) }()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed listener accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("server goroutine leaked")
	}
}

func TestServeHTTPClosesListenerForAlreadyCanceledContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serveHTTP(ctx, &http.Server{}, listener, time.Second); err != nil {
		t.Fatal(err)
	}
	connection, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("canceled startup left a listening socket")
	}
}
