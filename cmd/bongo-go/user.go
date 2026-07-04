package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/bongo-go/bongo-go/internal/security"
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

func init() {
	userCreateCmd.Flags().StringVar(&userEmail, "email", "", "User email address (required)")
	userCreateCmd.Flags().StringVar(&userPassword, "password", "", "User password (required)")
	userCreateCmd.Flags().StringVar(&userHTTPAddr, "http-addr", "http://127.0.0.1:8080", "HTTP address of a running node")
	userCreateCmd.Flags().StringVar(&userSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")
	userCreateCmd.Flags().BoolVar(&userAdmin, "admin", false, "Grant the new user administrator privileges")
	userCreateCmd.MarkFlagRequired("email")
	userCreateCmd.MarkFlagRequired("password")

	userCmd.AddCommand(userCreateCmd)
	rootCmd.AddCommand(userCmd)
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	body, _ := json.Marshal(map[string]any{
		"email":    userEmail,
		"password": userPassword,
		"admin":    userAdmin,
	})

	secret := userSessionSecret
	if secret == "" {
		secret = os.Getenv("BONGO_WEB_SESSION_SECRET")
	}

	req, err := http.NewRequest(http.MethodPost, userHTTPAddr+"/internal/user/create", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		security.SignInternalRequest(req, []byte(secret))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, respBody)
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
