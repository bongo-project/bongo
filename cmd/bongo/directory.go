package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
)

// directoryOpts holds the flags shared by `user list`, `domain list` and
// `alias list`.
type directoryOpts struct {
	httpAddr  string
	secret    string
	domain    string
	jsonOut   bool
	withScope bool // command has a --domain filter
}

func (o *directoryOpts) addFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.httpAddr, "http-addr", "127.0.0.1:8081", "address of the local node's control listener (server.control_addr); host:port or http://host:port")
	cmd.Flags().StringVar(&o.secret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "print a JSON array instead of a table")
	if o.withScope {
		cmd.Flags().StringVar(&o.domain, "domain", "", "only list entries on this email domain")
	}
	// The unreachable-server message is one line, printed once by Execute.
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
}

// controlAddr reduces --http-addr to host:port, accepting the URL form that
// `user create` uses.
func controlAddr(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "http://")
	if strings.Contains(s, "://") {
		return "", fmt.Errorf("control address %q: the control listener is plain HTTP, give host:port or http://host:port", s)
	}
	return strings.TrimRight(s, "/"), nil
}

// fetchDirectory GETs path from the control listener and returns the items,
// whether the server truncated the list, and any error. The query is final
// before signing because the signature covers the request URI.
func fetchDirectory[T any](o *directoryOpts, path string) ([]T, bool, error) {
	addr, err := controlAddr(o.httpAddr)
	if err != nil {
		return nil, false, err
	}
	q := url.Values{}
	if o.domain != "" {
		q.Set("domain", o.domain)
	}
	u := url.URL{Scheme: "http", Host: addr, Path: path, RawQuery: q.Encode()}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("building request: %w", err)
	}
	secret := o.secret
	if secret == "" {
		secret = os.Getenv("BONGO_WEB_SESSION_SECRET")
	}
	if secret != "" {
		security.SignInternalRequest(req, []byte(secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, false, fmt.Errorf("cannot reach bongo control listener at %s: %s", addr, oneLine(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, fmt.Errorf("reading response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, false, errors.New("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	default:
		return nil, false, fmt.Errorf("server returned %s: %s", resp.Status, oneLine(string(body)))
	}

	var out struct {
		Truncated bool `json:"truncated"`
		Items     []T  `json:"items"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false, fmt.Errorf("decoding response: %w", err)
	}
	return out.Items, out.Truncated, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// printDirectory writes items as a JSON array or as a table, then reports
// truncation as an error so scripts notice a partial list.
func printDirectory[T any](cmd *cobra.Command, o *directoryOpts, items []T, truncated bool, header []string, row func(T) []string) error {
	out := cmd.OutOrStdout()
	if o.jsonOut {
		if items == nil {
			items = []T{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
	} else {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
		for _, it := range items {
			_, _ = fmt.Fprintln(tw, strings.Join(row(it), "\t"))
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
	}
	if truncated {
		return fmt.Errorf("list truncated at %d rows by the server; use --domain to narrow it", store.DirectoryListLimit)
	}
	return nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func quotaCount(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return fmt.Sprint(n)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func quotaBytes(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return humanBytes(n)
}

func userRow(u store.UserSummary) []string {
	admin := "-"
	if len(u.AdminDomains) > 0 {
		admin = strings.Join(u.AdminDomains, ",")
	}
	return []string{u.Email, dash(u.DisplayName), enabledWord(!u.Disabled), yesNo(u.Admin), admin}
}

func domainRow(d store.DomainSummary) []string {
	return []string{
		d.Name, enabledWord(d.Enabled), dash(d.AliasOf), dash(d.CatchAll),
		quotaCount(d.MaxUsers), quotaBytes(d.MaxStorageBytes), humanBytes(d.UsageBytes), yesNo(d.DKIM),
	}
}

func aliasRow(a store.AddressAliasSummary) []string {
	return []string{a.Address, dash(a.Target)}
}
