package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func smtpExit(t *testing.T, cfg Config, opts smtpOpts) (code int, stdout, stderr string) {
	t.Helper()
	sched, err := validateSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var so, se bytes.Buffer
	code = smtpMain(context.Background(), cfg, sched, opts, &so, &se)
	return code, so.String(), se.String()
}

// TestSMTPExitCodes separates a run that says nothing about the server (3)
// from a server that could not keep up (4), and lets a ramp miss a step
// without failing.
func TestSMTPExitCodes(t *testing.T) {
	t.Run("a saturated generator is invalid", func(t *testing.T) {
		ts := newTestServer(t)
		cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 100, Duration: 2500 * time.Millisecond})
		opts := looseOpts()
		opts.lagThreshold = 250 * time.Millisecond
		opts.tickHook = func() { time.Sleep(400 * time.Millisecond) }
		code, _, stderr := smtpExit(t, cfg, opts)
		if code != exitRunInvalid || !strings.Contains(stderr, "INVALID") {
			t.Fatalf("exit %d, stderr %q; want %d", code, stderr, exitRunInvalid)
		}
	})
	t.Run("a fixed rate the server cannot hold is exit 4", func(t *testing.T) {
		ts := newTestServer(t)
		cfg := testConfig(t, ts.addr, "lag", smtpConfig{Rate: 100, Duration: 2 * time.Second, Workers: 2})
		code, _, stderr := smtpExit(t, cfg, looseOpts())
		if code != exitServerBehind || !strings.Contains(stderr, "could not hold") {
			t.Fatalf("exit %d, stderr %q; want %d", code, stderr, exitServerBehind)
		}
	})
	t.Run("a ramp step that misses its rate is a stderr line, not a failure", func(t *testing.T) {
		ts := newTestServer(t)
		cfg := testConfig(t, ts.addr, "lag", smtpConfig{Ramp: "100:50:1s:150", Duration: 2500 * time.Millisecond, Workers: 2})
		code, _, stderr := smtpExit(t, cfg, looseOpts())
		if code != exitOK {
			t.Fatalf("exit %d, stderr %q; a ramp finds the limit, it does not fail on it", code, stderr)
		}
		if !strings.Contains(stderr, "ramp step missed its rate: target 100.0/s") {
			t.Fatalf("no line for the missed step: %q", stderr)
		}
	})
	t.Run("a healthy fixed rate is exit 0 and prints nothing", func(t *testing.T) {
		ts := newTestServer(t)
		cfg := testConfig(t, ts.addr, "ok", smtpConfig{Rate: 50, Duration: time.Second})
		code, _, stderr := smtpExit(t, cfg, looseOpts())
		if code != exitOK || stderr != "" {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
	})
}
