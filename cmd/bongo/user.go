package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/security"
	"github.com/bongo-project/bongo/internal/store"
)

var (
	userEmail         string
	userPassword      string
	userHTTPAddr      string
	userSessionSecret string
	userAdmin         bool
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage users",
}

var userCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new user",
	RunE:  runUserCreate,
}

var userListOpts = directoryOpts{withScope: true}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List accounts (read-only)",
	Long: "Lists every account: email, display name, enabled or disabled, global admin and\n" +
		"the domains the account administers (as owner or domain admin). Reads the\n" +
		"node's Pebble store through the control listener. At most 100000 rows are\n" +
		"returned; a longer list is cut, a warning is printed and the exit code is\n" +
		"non-zero. Use --domain to narrow it.",
	Args: cobra.NoArgs,
	RunE: runUserList,
}

func runUserList(cmd *cobra.Command, _ []string) error {
	items, trunc, err := fetchDirectory[store.UserSummary](&userListOpts, "/internal/users")
	if err != nil {
		return err
	}
	return printDirectory(cmd, &userListOpts, items, trunc,
		[]string{"EMAIL", "NAME", "STATUS", "ADMIN", "ADMIN DOMAINS"}, userRow)
}

func init() {
	userListOpts.addFlags(userListCmd)
	userCmd.AddCommand(userListCmd)

	userCreateCmd.Flags().StringVar(&userEmail, "email", "", "User email address (required)")
	userCreateCmd.Flags().StringVar(&userPassword, "password", "", "User password (required)")
	userCreateCmd.Flags().StringVar(&userHTTPAddr, "http-addr", "http://127.0.0.1:8081", "URL of the running node's control listener (server.control_addr)")
	userCreateCmd.Flags().StringVar(&userSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")
	userCreateCmd.Flags().BoolVar(&userAdmin, "admin", false, "Grant the new user administrator privileges")
	if err := userCreateCmd.MarkFlagRequired("email"); err != nil {
		panic(err)
	}
	if err := userCreateCmd.MarkFlagRequired("password"); err != nil {
		panic(err)
	}

	userCmd.AddCommand(userCreateCmd)

	userReset2FACmd.Flags().StringVar(&userEmail, "email", "", "Primary address of the account (required)")
	userReset2FACmd.Flags().StringVar(&userHTTPAddr, "http-addr", "http://127.0.0.1:8081", "URL of the running node's control listener (server.control_addr)")
	userReset2FACmd.Flags().StringVar(&userSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")
	if err := userReset2FACmd.MarkFlagRequired("email"); err != nil {
		panic(err)
	}
	userCmd.AddCommand(userReset2FACmd)

	rootCmd.AddCommand(userCmd)
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	status, respBody, err := postControl("/internal/user/create", map[string]any{
		"email":    userEmail,
		"password": userPassword,
		"admin":    userAdmin,
	})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("server returned %d: %s", status, respBody)
	}

	var result struct {
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	fmt.Printf("user created: %s (id: %s)\n", userEmail, result.UserID)
	return nil
}

var userReset2FACmd = &cobra.Command{
	Use:   "reset-2fa",
	Short: "Turn off two-factor authentication for a locked-out account",
	Long: "Turns off two-factor authentication for the account whose primary address is\n" +
		"--email, for a user who has lost both the authenticator and the recovery codes,\n" +
		"or when no administrator can sign in. It clears the authenticator secret, the\n" +
		"recovery codes and the require-app-passwords switch, and signs the account out\n" +
		"of every web session. App passwords keep working. Runs against the node's\n" +
		"control listener, like user create.",
	Args: cobra.NoArgs,
	RunE: runUserReset2FA,
	// A refusal is one line, printed once by Execute; the usage is no help.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func runUserReset2FA(cmd *cobra.Command, _ []string) error {
	status, respBody, err := postControl("/internal/user/reset-2fa", map[string]any{"email": userEmail})
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		fmt.Fprintf(cmd.OutOrStdout(), "two-factor authentication turned off: %s\n", userEmail)
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("no account has the primary address %s", userEmail)
	case http.StatusConflict:
		return fmt.Errorf("two-factor authentication is not on for %s", userEmail)
	default:
		return fmt.Errorf("server returned %d: %s", status, bytes.TrimSpace(respBody))
	}
}

// postControl sends a signed JSON POST to the node's control listener and
// returns the status and body.
func postControl(path string, payload any) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("encoding request: %w", err)
	}

	secret := userSessionSecret
	if secret == "" {
		secret = os.Getenv("BONGO_WEB_SESSION_SECRET")
	}

	req, err := http.NewRequest(http.MethodPost, userHTTPAddr+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		security.SignInternalRequest(req, []byte(secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return 0, nil, fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("reading response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}
