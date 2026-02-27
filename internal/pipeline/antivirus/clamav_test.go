package antivirus

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// mockClamd starts a TCP listener that implements the INSTREAM protocol and
// responds with the provided response string after reading the full stream.
// It returns the listener address and a WaitGroup that is done after the
// first connection is fully handled.
func mockClamd(t *testing.T, response string) (addr string, wg *sync.WaitGroup) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mockClamd: listen: %v", err)
	}

	wg = &sync.WaitGroup{}
	wg.Add(1)

	t.Cleanup(func() { ln.Close() })

	go func() {
		defer wg.Done()

		conn, err := ln.Accept()
		if err != nil {
			// Listener closed by cleanup; ignore.
			return
		}
		defer conn.Close()

		// Use a buffered reader so we can read the null-terminated command
		// without over-consuming bytes belonging to the subsequent INSTREAM data.
		br := bufio.NewReader(conn)

		// Read and discard the command (null-terminated).
		if _, err := br.ReadString('\x00'); err != nil {
			return
		}

		// Read INSTREAM chunks until zero-length terminator.
		var lenBuf [4]byte
		for {
			if _, err := io.ReadFull(br, lenBuf[:]); err != nil {
				return
			}
			chunkLen := binary.BigEndian.Uint32(lenBuf[:])
			if chunkLen == 0 {
				break
			}
			// Discard chunk data.
			if _, err := io.CopyN(io.Discard, br, int64(chunkLen)); err != nil {
				return
			}
		}

		// Send the response.
		conn.Write([]byte(response + "\n"))
	}()

	return ln.Addr().String(), wg
}

// newTestEnvelope returns a minimal QueueEnvelope for test use.
func newTestEnvelope() *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:         "test-id",
		Sender:     "sender@example.com",
		Recipients: []string{"rcpt@example.com"},
	}
}

// silentLogger returns a slog.Logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestClamAVName(t *testing.T) {
	p := NewClamAVProcessor()
	if got := p.Name(); got != "antivirus" {
		t.Errorf("Name() = %q, want %q", got, "antivirus")
	}
}

func TestClamAVClean(t *testing.T) {
	addr, wg := mockClamd(t, "stream: OK")
	host, port := splitHostPort(t, addr)

	p := NewClamAVProcessor(
		WithTCP(host, port),
		WithTimeout(5*time.Second),
		WithLogger(silentLogger()),
	)

	result, err := p.Process(context.Background(), newTestEnvelope(), strings.NewReader("clean message body"))
	wg.Wait()

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("Action = %q, want %q", result.Action, "pass")
	}
	if result.Stage != "antivirus" {
		t.Errorf("Stage = %q, want %q", result.Stage, "antivirus")
	}
	if result.Backend != "clamav" {
		t.Errorf("Backend = %q, want %q", result.Backend, "clamav")
	}
	if result.Score != 0 {
		t.Errorf("Score = %v, want 0", result.Score)
	}
}

func TestClamAVVirusFound(t *testing.T) {
	addr, wg := mockClamd(t, "stream: Eicar-Test-Signature FOUND")
	host, port := splitHostPort(t, addr)

	p := NewClamAVProcessor(
		WithTCP(host, port),
		WithTimeout(5*time.Second),
		WithLogger(silentLogger()),
	)

	result, err := p.Process(context.Background(), newTestEnvelope(), strings.NewReader("virus message body"))
	wg.Wait()

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Action != "quarantine" {
		t.Errorf("Action = %q, want %q", result.Action, "quarantine")
	}
	if result.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", result.Score)
	}
	if len(result.Reasons) == 0 {
		t.Fatal("Reasons is empty, want virus name in reasons")
	}
	if !strings.Contains(result.Reasons[0], "Eicar-Test-Signature") {
		t.Errorf("Reasons[0] = %q, want it to contain %q", result.Reasons[0], "Eicar-Test-Signature")
	}
}

func TestClamAVUnreachable(t *testing.T) {
	// Connect to a port that has no listener — guaranteed to fail immediately.
	p := NewClamAVProcessor(
		WithTCP("127.0.0.1", 1), // port 1 is always refused
		WithTimeout(2*time.Second),
		WithLogger(silentLogger()),
	)

	result, err := p.Process(context.Background(), newTestEnvelope(), strings.NewReader("some message"))

	if err != nil {
		t.Fatalf("Process() returned error = %v, want nil (graceful fallback)", err)
	}
	if result.Action != "pass" {
		t.Errorf("Action = %q, want %q (graceful fallback)", result.Action, "pass")
	}
	// Reasons should indicate unavailability.
	if len(result.Reasons) == 0 {
		t.Error("Reasons is empty, want a reason explaining clamd is unavailable")
	}
}

func TestClamAVLargeMessage(t *testing.T) {
	addr, wg := mockClamd(t, "stream: OK")
	host, port := splitHostPort(t, addr)

	p := NewClamAVProcessor(
		WithTCP(host, port),
		WithTimeout(10*time.Second),
		WithLogger(silentLogger()),
	)

	// Build a message larger than one 2048-byte chunk (e.g., 10 KB).
	largeBody := strings.Repeat("A", 10*1024)

	result, err := p.Process(context.Background(), newTestEnvelope(), strings.NewReader(largeBody))
	wg.Wait()

	if err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if result.Action != "pass" {
		t.Errorf("Action = %q, want %q", result.Action, "pass")
	}
}

// splitHostPort is a test helper that splits addr into host and port int.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	var port int
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitHostPort: %v", err)
	}
	if _, err = fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("splitHostPort: parse port %q: %v", portStr, err)
	}
	return host, port
}
