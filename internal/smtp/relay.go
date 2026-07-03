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
	tlsCompleted := false
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			r.logger.Debug("relay: STARTTLS failed, continuing without", "host", host, "error", err)
		} else {
			tlsCompleted = true
		}
	}

	// Authenticate if credentials are configured.
	hasCreds := r.cfg != nil && r.cfg.Username != ""
	allowInsecure := r.cfg != nil && r.cfg.AllowInsecureAuth
	send, authErr := shouldSendAuth(tlsCompleted, hasCreds, allowInsecure)
	if authErr != nil {
		return nil, fmt.Errorf("relay AUTH guard for %s: %w", addr, authErr)
	}
	if send {
		auth := &plainAuth{username: r.cfg.Username, password: r.cfg.Password}
		if err := client.Auth(auth); err != nil {
			return nil, fmt.Errorf("relay AUTH for %s: %w", addr, err)
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

// shouldSendAuth decides whether AUTH credentials should be transmitted.
//
//   - hasCreds: a relay username/password is configured.
//   - tlsCompleted: STARTTLS negotiation succeeded for this connection.
//   - allowInsecure: operator opt-in to send credentials over plaintext
//     (smtp.relay.allow_insecure_auth = true).
//
// Returns (true, nil) when auth should proceed, (false, nil) when no
// credentials are configured, and (false, err) when credentials are
// configured but the connection is unencrypted and the operator has not
// explicitly opted in.
func shouldSendAuth(tlsCompleted, hasCreds, allowInsecure bool) (bool, error) {
	if !hasCreds {
		return false, nil
	}
	if tlsCompleted || allowInsecure {
		return true, nil
	}
	return false, fmt.Errorf("relay: refusing to send AUTH over an unencrypted connection to smart host (set smtp.relay.allow_insecure_auth = true to override)")
}

// plainAuth implements smtp.Auth using the PLAIN mechanism.
// Credentials are only transmitted after shouldSendAuth confirms it is safe
// to do so (STARTTLS completed or the operator has explicitly opted in via
// smtp.relay.allow_insecure_auth).
type plainAuth struct {
	username string
	password string
}

func (a *plainAuth) Start(server *netsmtp.ServerInfo) (string, []byte, error) {
	resp := []byte("\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *plainAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	return nil, nil
}
