package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"syscall"
	"testing"
	"time"
)

// These tests send real signals to the test process, so they must not run in
// parallel with each other or with anything else that sends signals.

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sendSelf(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatalf("kill self: %v", err)
	}
}

func TestWatchSignals_HUPReloadsWithoutCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reloads := make(chan struct{}, 4)
	stop := watchSignals(ctx, cancel, func(context.Context) error {
		reloads <- struct{}{}
		return nil
	}, quietLogger())
	defer stop()

	sendSelf(t, syscall.SIGHUP)
	select {
	case <-reloads:
	case <-time.After(2 * time.Second):
		t.Fatal("SIGHUP did not call reload")
	}
	select {
	case <-ctx.Done():
		t.Fatal("SIGHUP cancelled the context")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchSignals_TermSignalsCancel(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reloaded := make(chan struct{}, 1)
			stop := watchSignals(ctx, cancel, func(context.Context) error {
				reloaded <- struct{}{}
				return nil
			}, quietLogger())
			defer stop()

			sendSelf(t, sig)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				t.Fatalf("%v did not cancel the context", sig)
			}
			select {
			case <-reloaded:
				t.Errorf("%v triggered a reload", sig)
			default:
			}
		})
	}
}

func TestWatchSignals_StopExitsGoroutines(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stop := watchSignals(ctx, cancel, func(context.Context) error { return nil }, quietLogger())

	cancel()
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop did not return: signal goroutines still running")
	}
}

func TestWatchSignals_StopWithoutCancelDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := watchSignals(ctx, cancel, func(context.Context) error { return nil }, quietLogger())
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop hung with ctx still live")
	}
}
