package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/security"
)

var (
	addressHTTPAddr      string
	addressSessionSecret string
)

var addressCmd = &cobra.Command{
	Use:   "address",
	Short: "Address index maintenance",
}

// The address index is consulted BEFORE aliases, the users table and the
// per-domain catch-all, so a record left behind by a deleted account silently
// outranks every later mapping for the same address and routes mail into an
// unreachable mailbox. These commands find and remove those records.
var addressOrphansCmd = &cobra.Command{
	Use:   "orphans",
	Short: "List address records whose owning account no longer exists (read-only)",
	RunE:  runAddressOrphans,
}

var addressRepairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Delete address records whose owning account no longer exists",
	RunE:  runAddressRepair,
}

func init() {
	addressCmd.PersistentFlags().StringVar(&addressHTTPAddr, "http-addr", "127.0.0.1:8081", "address of the local node's control listener (server.control_addr)")
	addressCmd.PersistentFlags().StringVar(&addressSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")

	addressCmd.AddCommand(addressOrphansCmd)
	addressCmd.AddCommand(addressRepairCmd)
	rootCmd.AddCommand(addressCmd)
}

func addressRequest(method, url string) (*http.Response, error) {
	secret := addressSessionSecret
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
		return nil, fmt.Errorf("connecting to %s: %w", addressHTTPAddr, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if err := resp.Body.Close(); err != nil {
			return nil, fmt.Errorf("closing response body: %w", err)
		}
		return nil, fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}
	return resp, nil
}

type orphanedAddressesResponse struct {
	Count   int `json:"count"`
	Orphans []struct {
		Address string `json:"Address"`
		UserID  string `json:"UserID"`
		Kind    string `json:"Kind"`
		Target  string `json:"Target"`
	} `json:"orphans"`
}

func runAddressOrphans(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/internal/addresses/orphaned", addressHTTPAddr)
	resp, err := addressRequest(http.MethodGet, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s: %s", resp.Status, string(body))
	}

	var out orphanedAddressesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if out.Count == 0 {
		fmt.Println("No orphaned address records.")
		return nil
	}
	fmt.Printf("%d orphaned address record(s):\n", out.Count)
	reportOnly := false
	for _, o := range out.Orphans {
		switch o.Kind {
		case "legacy-alias":
			reportOnly = true
			fmt.Printf("  %s -> %s (kind=%s, target has no account)\n", o.Address, o.Target, o.Kind)
		case "catchall":
			reportOnly = true
			fmt.Printf("  %s -> %s (kind=%s, account no longer exists)\n", o.Address, o.UserID, o.Kind)
		default:
			fmt.Printf("  %s -> %s (kind=%s, account no longer exists)\n", o.Address, o.UserID, o.Kind)
		}
	}
	fmt.Println("\nMail to address records resolves to a deleted account and is delivered")
	fmt.Println("into an unreachable mailbox. Run 'bongo address repair' to remove them.")
	if reportOnly {
		fmt.Println("Legacy-alias and catchall entries are report-only: remove those keys by hand.")
	}
	return nil
}

func runAddressRepair(cmd *cobra.Command, args []string) error {
	url := fmt.Sprintf("http://%s/internal/addresses/repair", addressHTTPAddr)
	resp, err := addressRequest(http.MethodPost, url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s: %s", resp.Status, string(body))
	}

	var out struct {
		Count   int      `json:"count"`
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if out.Count == 0 {
		fmt.Println("No orphaned address records to remove.")
		return nil
	}
	fmt.Printf("Removed %d orphaned address record(s):\n", out.Count)
	for _, a := range out.Deleted {
		fmt.Printf("  %s\n", a)
	}
	return nil
}
