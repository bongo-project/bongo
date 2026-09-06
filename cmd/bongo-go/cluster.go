package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/cluster"
	"github.com/bongo-project/bongo/internal/security"
)

var (
	clusterHTTPAddr      string
	clusterSessionSecret string
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster management commands",
}

var clusterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster membership",
	Long:  "Query the local node's memberlist and display cluster members.",
	RunE:  runClusterStatus,
}

// A snapshot is otherwise only taken when the raft log passes
// cluster.raft.snapshot_threshold. On a low-volume node that threshold can be
// weeks of writes away, so every restart restores from a long-stale snapshot
// and replays a log that never truncates. This forces one on demand.
var clusterSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Force the leader to take a Raft snapshot now",
	Long: "Trigger an immediate Raft snapshot on the leader, without waiting for\n" +
		"cluster.raft.snapshot_threshold to be reached. Must be run against the\n" +
		"leader node; a follower reports which state it is in instead.",
	RunE: runClusterSnapshot,
}

func init() {
	clusterCmd.PersistentFlags().StringVar(&clusterHTTPAddr, "http-addr", "127.0.0.1:8080", "HTTP address of the local node")
	// Persistent on clusterCmd: both subcommands call auth-gated /internal/
	// endpoints, so the flag belongs to the group rather than one member.
	clusterCmd.PersistentFlags().StringVar(&clusterSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")

	clusterCmd.AddCommand(clusterStatusCmd)
	clusterCmd.AddCommand(clusterSnapshotCmd)
	rootCmd.AddCommand(clusterCmd)
}

func runClusterStatus(cmd *cobra.Command, args []string) error {
	if _, _, err := net.SplitHostPort(clusterHTTPAddr); err != nil {
		return fmt.Errorf("invalid --http-addr %q: %w", clusterHTTPAddr, err)
	}
	url := fmt.Sprintf("http://%s/internal/cluster/members", clusterHTTPAddr)

	// Signed, not a bare http.Get. /internal/cluster/members sits behind
	// RequireInternalAuth, so an unsigned request returns 401 on any node with
	// web.session_secret set -- which is every production node. This command
	// was unusable there until the signing was added.
	resp, err := clusterRequest(http.MethodGet, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var members []cluster.MemberInfo
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if len(members) == 0 {
		fmt.Println("No cluster members found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "Node\tAddress\tRole\tStatus")
	for _, m := range members {
		addr := fmt.Sprintf("%s:%d", m.Addr, m.Port)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", m.Name, addr, m.Role, m.Status)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing output: %w", err)
	}

	return nil
}

// clusterRequest performs an HMAC-signed call to an /internal/ endpoint on the
// local node, following the same shape as addressRequest and queueRequest.
//
// Deliberately a fourth copy rather than a shared helper: keeping this change
// confined to the raft fix was worth more than the deduplication, and each
// command owns its own --session-secret and --http-addr flags. If these are
// ever unified, all four should move together.
func clusterRequest(method, url string) (*http.Response, error) {
	secret := clusterSessionSecret
	if secret == "" {
		secret = os.Getenv("BONGO_WEB_SESSION_SECRET")
	}

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	if secret != "" {
		security.SignInternalRequest(req, []byte(secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", clusterHTTPAddr, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if err := resp.Body.Close(); err != nil {
			return nil, fmt.Errorf("closing response body: %w", err)
		}
		return nil, fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}
	return resp, nil
}

func runClusterSnapshot(cmd *cobra.Command, args []string) error {
	if _, _, err := net.SplitHostPort(clusterHTTPAddr); err != nil {
		return fmt.Errorf("invalid --http-addr %q: %w", clusterHTTPAddr, err)
	}
	url := fmt.Sprintf("http://%s/internal/cluster/snapshot", clusterHTTPAddr)

	resp, err := clusterRequest(http.MethodPost, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	var out struct {
		Status     string `json:"status"`
		Error      string `json:"error"`
		DurationMS int64  `json:"duration_ms"`
	}
	// A non-JSON body (a proxy error page, say) should still surface the
	// status line rather than a decode error.
	if jsonErr := json.Unmarshal(body, &out); jsonErr != nil {
		return fmt.Errorf("server returned %s: %s", resp.Status, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("snapshot not taken (%s): %s", resp.Status, out.Error)
	}

	fmt.Printf("Snapshot: %s (%dms)\n", out.Status, out.DurationMS)
	return nil
}
