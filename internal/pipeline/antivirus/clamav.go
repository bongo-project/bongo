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

	"github.com/bongo-go/bongo-go/internal/pipeline"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	defaultSocket    = "/var/run/clamav/clamd.ctl"
	defaultTimeout   = 60 * time.Second
	instreamChunkMax = 2048
)

// ClamAVProcessor implements pipeline.Processor by scanning messages via
// the clamd INSTREAM protocol. If clamd is unreachable the processor logs a
// warning and returns "pass" so that mail delivery is never blocked by an
// unavailable AV service.
type ClamAVProcessor struct {
	network string
	address string
	timeout time.Duration
	logger  *slog.Logger
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

// NewClamAVProcessor returns a ClamAVProcessor configured with opts.
// Defaults: Unix socket /var/run/clamav/clamd.ctl, 60s timeout.
func NewClamAVProcessor(opts ...Option) *ClamAVProcessor {
	p := &ClamAVProcessor{
		network: "unix",
		address: defaultSocket,
		timeout: defaultTimeout,
		logger:  slog.Default(),
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
// On connection failure the processor logs a warning and returns "pass" to
// avoid blocking mail delivery.
func (p *ClamAVProcessor) Process(ctx context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()

	response, err := p.scan(ctx, msg)
	if err != nil {
		// Graceful fallback: log and pass so delivery is never blocked.
		p.logger.Warn("antivirus: clamd unreachable, skipping scan",
			"network", p.network,
			"address", p.address,
			"error", err,
		)
		return &bongov1.StageResult{
			Stage:       "antivirus",
			Backend:     "clamav",
			Action:      "pass",
			Score:       0,
			Reasons:     []string{"clamd unavailable"},
			ProcessedAt: start.Unix(),
			DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
		}, nil
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
		p.logger.Warn("antivirus: unexpected clamd response",
			"response", response,
		)
		reasons = []string{fmt.Sprintf("unexpected response: %s", response)}
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
