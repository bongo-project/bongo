package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

var (
	userEmail    string
	userPassword string
	userHTTPAddr string
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
	userCreateCmd.MarkFlagRequired("email")
	userCreateCmd.MarkFlagRequired("password")

	userCmd.AddCommand(userCreateCmd)
	rootCmd.AddCommand(userCmd)
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	body, _ := json.Marshal(map[string]string{
		"email":    userEmail,
		"password": userPassword,
	})

	resp, err := http.Post(userHTTPAddr+"/internal/user/create", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("connecting to server: %w", err)
	}
	defer resp.Body.Close()

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
