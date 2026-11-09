package httpmw

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// When graceful shutdown overruns its budget, Serve reports it and drops the remaining connections instead of leaving them open.
func TestServeClosesConnectionsWhenGracefulShutdownTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	started := make(chan struct{})
	var startedOnce sync.Once
	srv := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-r.Context().Done() // only ends when the connection is dropped
	})}
	log, _ := logger(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	served := make(chan error, 1)
	go func() { served <- Serve(ctx, log, "test", srv, ServeOptions{ShutdownTimeout: 100 * time.Millisecond}) }()

	resp := make(chan error, 1)
	client := &http.Client{Timeout: 3 * time.Second}
	go func() {
		for i := 0; i < 50; i++ {
			r, err := client.Get("http://" + addr + "/")
			if err == nil {
				r.Body.Close()
			}
			if err != nil && !strings.Contains(err.Error(), "refused") {
				resp <- err
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		resp <- nil
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the handler")
	}
	cancel()

	select {
	case err := <-served:
		if err == nil || !strings.Contains(err.Error(), "graceful shutdown") {
			t.Fatalf("Serve returned %v, want a graceful-shutdown error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return")
	}
	select {
	case err := <-resp:
		if err == nil {
			t.Fatal("the in-flight request completed normally; its connection should have been dropped")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight connection stayed open after shutdown")
	}
}
