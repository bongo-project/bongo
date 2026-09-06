package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

var (
	queueHTTPAddr      string
	queueJSON          bool
	queueSessionSecret string
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Queue management commands",
}

var queueStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show queue status",
	RunE:  runQueueStatus,
}

var queueRetryCmd = &cobra.Command{
	Use:   "retry [id]",
	Short: "Retry a failed message",
	Args:  cobra.ExactArgs(1),
	RunE:  runQueueRetry,
}

var queueDropCmd = &cobra.Command{
	Use:   "drop [id]",
	Short: "Drop a message from the queue",
	Args:  cobra.ExactArgs(1),
	RunE:  runQueueDrop,
}

func init() {
	queueCmd.PersistentFlags().StringVar(&queueHTTPAddr, "http-addr", "127.0.0.1:8080", "HTTP address of the local node")
	queueCmd.PersistentFlags().BoolVar(&queueJSON, "json", false, "Output as JSON")
	queueCmd.PersistentFlags().StringVar(&queueSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")

	queueCmd.AddCommand(queueStatusCmd)
	queueCmd.AddCommand(queueRetryCmd)
	queueCmd.AddCommand(queueDropCmd)
	rootCmd.AddCommand(queueCmd)
}

// queueRequest builds a signed request to an /internal/queue endpoint. The
// endpoints are guarded by RequireInternalAuth, so the request must carry an
// HMAC signature derived from the node's web.session_secret.
func queueRequest(method, url string) (*http.Response, error) {
	return signedInternalRequest(method, url, queueHTTPAddr, queueSessionSecret)
}

func runQueueStatus(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/internal/queue/stats", queueHTTPAddr)

	resp, err := queueRequest(http.MethodGet, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	var stats bongov1.QueueStats
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if queueJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(&stats)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "Pending:\t%d\n", stats.PendingCount)
	fmt.Fprintf(w, "Bounced:\t%d\n", stats.FailedCount)
	if stats.OldestPendingSeconds > 0 {
		fmt.Fprintf(w, "Oldest:\t%dm ago\n", stats.OldestPendingSeconds/60)
	} else {
		fmt.Fprintf(w, "Oldest:\t-\n")
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flushing output: %w", err)
	}

	return nil
}

func runQueueRetry(cmd *cobra.Command, args []string) error {
	id := args[0]
	url := fmt.Sprintf("http://%s/internal/queue/%s/retry", queueHTTPAddr, id)

	resp, err := queueRequest(http.MethodPost, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	if queueJSON {
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
		return nil
	}

	fmt.Printf("Message %s scheduled for retry.\n", id)
	return nil
}

func runQueueDrop(cmd *cobra.Command, args []string) error {
	id := args[0]
	url := fmt.Sprintf("http://%s/internal/queue/%s", queueHTTPAddr, id)

	resp, err := queueRequest(http.MethodDelete, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}

	if queueJSON {
		if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
			return fmt.Errorf("writing response: %w", err)
		}
		return nil
	}

	fmt.Printf("Message %s dropped from queue.\n", id)
	return nil
}
