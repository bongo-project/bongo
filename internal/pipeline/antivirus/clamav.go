// Package antivirus provides a pipeline Processor for ClamAV virus scanning
// via the clamd protocol.
package antivirus

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/bongo-project/bongo/internal/pipeline"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

const (
	defaultSocket    = "/var/run/clamav/clamd.ctl"
	defaultTimeout   = 60 * time.Second
	instreamChunkMax = 2048
)

// OnFailure selects what a ClamAVProcessor does when it cannot get a
// definitive clean/infected verdict from clamd — either because clamd is
// unreachable, or because clamd returned an error response (e.g. the
// INSTREAM size limit or an internal fault).
type OnFailure string

const (
	// OnFailureQuarantine (the default) quarantines the message. It is not
	// delivered unscanned. This is the safer default: a quarantined message
	// is drainable via the admin bulk rescan action (internal/pipeline/rescan.go
	// PrepareForRescan) once clamd is reachable again, whereas a message
	// delivered unscanned cannot be recalled.
	OnFailureQuarantine OnFailure = "quarantine"
	// OnFailureAccept delivers the message unscanned. Use only when
	// availability is valued over the guarantee that every message is
	// scanned; every occurrence is logged at ERROR.
	OnFailureAccept OnFailure = "accept"
)

// ClamAVProcessor implements pipeline.Processor by scanning messages via
// the clamd INSTREAM protocol. When clamd is unreachable or returns an error
// response, the configured OnFailure policy decides whether the message is
// quarantined (default) or delivered unscanned ("accept"); either way the
// failure is logged at ERROR. Mail is never silently delivered unscanned
// without that ERROR log line.
type ClamAVProcessor struct {
	network   string
	address   string
	timeout   time.Duration
	logger    *slog.Logger
	onFailure OnFailure
}

// Option is a functional option for ClamAVProcessor.
type Option func(*ClamAVProcessor)

// WithSocket configures the processor to connect to clamd via a Unix domain
// socket at path.
func WithSocket(path string) Option {
	return func(p *ClamAVProcessor) {
		p.network = "unix"
		p.address = path
	}
}

// WithTCP configures the processor to connect to clamd via TCP.
func WithTCP(host string, port int) Option {
	return func(p *ClamAVProcessor) {
		p.network = "tcp"
		p.address = fmt.Sprintf("%s:%d", host, port)
	}
}

// WithTimeout sets the connection and I/O timeout (default 60s).
func WithTimeout(d time.Duration) Option {
	return func(p *ClamAVProcessor) {
		p.timeout = d
	}
}

// WithLogger sets the structured logger used for diagnostic output.
func WithLogger(l *slog.Logger) Option {
	return func(p *ClamAVProcessor) {
		p.logger = l
	}
}

// WithOnFailure sets the policy applied when clamd is unreachable or returns
// an error response. Defaults to OnFailureQuarantine.
func WithOnFailure(policy OnFailure) Option {
	return func(p *ClamAVProcessor) {
		p.onFailure = policy
	}
}

// NewClamAVProcessor returns a ClamAVProcessor configured with opts.
// Defaults: Unix socket /var/run/clamav/clamd.ctl, 60s timeout, on_failure
// quarantine.
func NewClamAVProcessor(opts ...Option) *ClamAVProcessor {
	p := &ClamAVProcessor{
		network:   "unix",
		address:   defaultSocket,
		timeout:   defaultTimeout,
		logger:    slog.Default(),
		onFailure: OnFailureQuarantine,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name returns the stage identifier.
func (p *ClamAVProcessor) Name() string {
	return "antivirus"
}

// Process connects to clamd, streams the message via the INSTREAM command,
// and returns a StageResult with action "pass" or "quarantine".
//
// On connection failure, or on any clamd response that is not a definitive
// clean/infected verdict, the configured OnFailure policy decides the outcome (OnFailureQuarantine by
// default, OnFailureAccept to deliver unscanned).
// Either way the failure is logged at ERROR — never Warn — because it means a
// message went through without being scanned for viruses, or was quarantined
// without one being found. A Go error is never returned for these cases: this
// is a scan verdict (StageResult), not a pipeline stage fault, and the runner
// treats a returned error very differently (moving the message to the
// unmonitored spool/error directory instead of the drainable quarantine/ —
// see internal/pipeline/runner.go).
func (p *ClamAVProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	response, err := p.scan(ctx, msg)
	if err != nil {
		return p.failureResult(start, env, fmt.Sprintf("scanner unavailable: %v", err),
			"network", p.network, "address", p.address, "error", err), nil
	}

	action := "pass"
	var reasons []string
	var score float32

	response = strings.TrimRight(response, "\n\r\x00")

	switch {
	case response == "stream: OK":
		// Clean message.
	case strings.HasSuffix(response, " FOUND"):
		// Format: "stream: {virus_name} FOUND"
		virusName := strings.TrimPrefix(response, "stream: ")
		virusName = strings.TrimSuffix(virusName, " FOUND")
		action = "quarantine"
		score = 1.0
		reasons = []string{fmt.Sprintf("virus detected: %s", virusName)}
		p.logger.Warn("antivirus: virus found",
			"virus", virusName,
			"sender", env.GetSender(),
		)
	default:
		// clamd emits an ERROR-suffixed response for conditions like the
		// INSTREAM size limit ("INSTREAM size limit exceeded. ERROR") or an
		// internal fault, and any other response is unrecognised. Neither is
		// a clean verdict, so treat it as a scan failure subject to the same
		// OnFailure policy as an unreachable clamd — silently delivering
		// unscanned mail here is exactly the failure mode this policy exists
		// to close.
		return p.failureResult(start, env, fmt.Sprintf("unrecognised clamd response: %s", response),
			"response", response), nil
	}

	p.logger.Info("antivirus: scan complete",
		"action", action,
		"sender", env.GetSender(),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	return &bongov1.StageResult{
		Stage:       "antivirus",
		Backend:     "clamav",
		Action:      action,
		Score:       score,
		Reasons:     reasons,
		ProcessedAt: start.Unix(),
		DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
	}, nil
}

// failureResult logs a scan failure at ERROR and builds the StageResult for
// it per the configured OnFailure policy. reason is used verbatim as the
// sole entry in Reasons — it is rendered directly in the admin quarantine UI
// (internal/web/handlers/quarantine.go), so it must never read like a virus
// was found (the runner sets the same MsgFlagVirus audit flag for any
// quarantine action, scan failure or genuine infection alike; the Reasons
// text is what tells the two apart for an operator).
func (p *ClamAVProcessor) failureResult(start time.Time, env *bongov1.QueueEnvelope, reason string, logArgs ...any) *bongov1.StageResult {
	action := "pass"
	if p.onFailure != OnFailureAccept {
		action = "quarantine"
	}

	p.logger.Error("antivirus: scan failed, applying on_failure policy",
		append([]any{"policy", string(p.onFailure), "action", action, "sender", env.GetSender()}, logArgs...)...,
	)

	return &bongov1.StageResult{
		Stage:       "antivirus",
		Backend:     "clamav",
		Action:      action,
		Score:       0,
		Reasons:     []string{reason},
		ProcessedAt: start.Unix(),
		DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
	}
}

// scan connects to clamd, sends INSTREAM, streams msg in chunks, and returns
// the raw clamd response string.
func (p *ClamAVProcessor) scan(ctx context.Context, msg io.Reader) (string, error) {
	dialer := &net.Dialer{Timeout: p.timeout}
	conn, err := dialer.DialContext(ctx, p.network, p.address)
	if err != nil {
		return "", fmt.Errorf("antivirus: connect to clamd: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(p.timeout)); err != nil {
		return "", fmt.Errorf("antivirus: set deadline: %w", err)
	}

	// Send the INSTREAM command. clamd expects a null-terminated command.
	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", fmt.Errorf("antivirus: write INSTREAM command: %w", err)
	}

	// Stream message content in chunks prefixed by 4-byte big-endian length.
	buf := make([]byte, instreamChunkMax)
	for {
		n, readErr := msg.Read(buf)
		if n > 0 {
			// Write the 4-byte length prefix.
			var lenBuf [4]byte
			binary.BigEndian.PutUint32(lenBuf[:], uint32(n)) // #nosec G115 -- n <= instreamChunkMax (2048)
			if _, err := conn.Write(lenBuf[:]); err != nil {
				return "", fmt.Errorf("antivirus: write chunk length: %w", err)
			}
			// Write the chunk data.
			if _, err := conn.Write(buf[:n]); err != nil {
				return "", fmt.Errorf("antivirus: write chunk data: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("antivirus: read message: %w", readErr)
		}
	}

	// Send zero-length chunk to signal end of stream.
	var zeroLen [4]byte
	if _, err := conn.Write(zeroLen[:]); err != nil {
		return "", fmt.Errorf("antivirus: write end marker: %w", err)
	}

	// Read clamd response. Response ends with a newline.
	response, err := io.ReadAll(conn)
	if err != nil {
		return "", fmt.Errorf("antivirus: read response: %w", err)
	}

	return string(response), nil
}
