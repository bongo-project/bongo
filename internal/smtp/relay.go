package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	netsmtp "net/smtp"
	"sort"
	"strings"
	"time"

	"github.com/bongo-go/bongo-go/internal/config"
)

// Relay delivers outbound messages to external SMTP servers.
type Relay struct {
	cfg    *config.SMTPRelay
	logger *slog.Logger
}

// NewRelay creates a new Relay.
func NewRelay(cfg *config.SMTPRelay, logger *slog.Logger) *Relay {
	return &Relay{cfg: cfg, logger: logger}
}

// Deliver sends a message to external recipients.
// Returns permanently failed recipients and any temporary error.
func (r *Relay) Deliver(ctx context.Context, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error) {
	if r.cfg != nil && r.cfg.Enabled && r.cfg.Host != "" {
		return r.deliverViaRelay(ctx, from, recipients, rawMsg)
	}
	return r.deliverViaMX(ctx, from, recipients, rawMsg)
}

func (r *Relay) deliverViaRelay(ctx context.Context, from string, recipients []string, rawMsg []byte) ([]string, error) {
	r.logger.Info("relay: delivering via smart relay", "host", r.cfg.Host, "recipients", len(recipients))
	return r.sendToHost(ctx, r.cfg.Host, from, recipients, rawMsg)
}

func (r *Relay) deliverViaMX(ctx context.Context, from string, recipients []string, rawMsg []byte) ([]string, error) {
	// Group recipients by domain.
	byDomain := make(map[string][]string)
	for _, rcpt := range recipients {
		parts := strings.SplitN(rcpt, "@", 2)
		if len(parts) != 2 {
			continue
		}
		domain := strings.ToLower(parts[1])
		byDomain[domain] = append(byDomain[domain], rcpt)
	}

	var allPermanentFails []string
	for domain, domainRcpts := range byDomain {
		mxRecords, err := net.LookupMX(domain)
		if err != nil {
			r.logger.Warn("relay: MX lookup failed", "domain", domain, "error", err)
			return nil, fmt.Errorf("MX lookup for %s: %w", domain, err)
		}

		if len(mxRecords) == 0 {
			// Fall back to A record.
			mxRecords = []*net.MX{{Host: domain, Pref: 0}}
		}

		// Sort by priority.
		sort.Slice(mxRecords, func(i, j int) bool {
			return mxRecords[i].Pref < mxRecords[j].Pref
		})

		var lastErr error
		for _, mx := range mxRecords {
			host := strings.TrimSuffix(mx.Host, ".")
			addr := net.JoinHostPort(host, "25")

			permFails, err := r.sendToHost(ctx, addr, from, domainRcpts, rawMsg)
			allPermanentFails = append(allPermanentFails, permFails...)
			if err == nil {
				lastErr = nil
				break
			}
			lastErr = err
			r.logger.Warn("relay: MX attempt failed, trying next", "host", host, "error", err)
		}
		if lastErr != nil {
			return allPermanentFails, fmt.Errorf("all MX hosts failed for %s: %w", domain, lastErr)
		}
	}

	return allPermanentFails, nil
}

func (r *Relay) sendToHost(ctx context.Context, addr, from string, recipients []string, rawMsg []byte) (permanentFails []string, err error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	conn, err := net.DialTimeout("tcp", addr, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}

	client, err := netsmtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("smtp client for %s: %w", addr, err)
	}
	defer client.Close()

	// Opportunistic STARTTLS.
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			r.logger.Debug("relay: STARTTLS failed, continuing without", "host", host, "error", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return nil, fmt.Errorf("MAIL FROM: %w", err)
	}

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			if isPermanentError(err) {
				permanentFails = append(permanentFails, rcpt)
				r.logger.Warn("relay: permanent recipient failure", "rcpt", rcpt, "error", err)
				continue
			}
			return permanentFails, fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}

	// If all recipients failed permanently, no need to send DATA.
	if len(permanentFails) == len(recipients) {
		client.Quit()
		return permanentFails, nil
	}

	wc, err := client.Data()
	if err != nil {
		return permanentFails, fmt.Errorf("DATA: %w", err)
	}

	if _, err := wc.Write(rawMsg); err != nil {
		wc.Close()
		return permanentFails, fmt.Errorf("writing message: %w", err)
	}

	if err := wc.Close(); err != nil {
		return permanentFails, fmt.Errorf("closing data: %w", err)
	}

	client.Quit()
	r.logger.Info("relay: delivered", "host", addr, "from", from, "recipients", len(recipients)-len(permanentFails))
	return permanentFails, nil
}

// isPermanentError checks if an SMTP error is a 5xx permanent failure.
func isPermanentError(err error) bool {
	errStr := err.Error()
	return len(errStr) >= 3 && errStr[0] == '5'
}
