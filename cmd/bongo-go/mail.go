package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/bongo-project/bongo/internal/security"
)

var (
	mailHTTPAddr      string
	mailSessionSecret string
	mailPurgeConfirm  bool
)

var mailCmd = &cobra.Command{
	Use:   "mail",
	Short: "Mail metadata maintenance",
}

// A blob GC that reclaims a still-referenced blob leaves mail metadata behind
// describing a message whose body no longer exists. The record is not an
// orphan, so the 15-minute orphan sweep passes over it forever, while every
// indexing run logs a failed body read for it. These commands report those
// records and, on demand, remove them.
var mailDanglingCmd = &cobra.Command{
	Use:   "dangling",
	Short: "List mail metadata whose message body blob is missing (read-only)",
	RunE:  runMailDangling,
}

var mailPurgeDanglingCmd = &cobra.Command{
	Use:   "purge-dangling",
	Short: "Delete mail metadata whose message body blob is missing",
	Long: "Deletes the metadata for messages whose body blob is gone, together with\n" +
		"every mailbox entry referencing them. The bodies are already unrecoverable;\n" +
		"this removes the unreadable entries they left behind. Irreversible, so it\n" +
		"requires --confirm. Run 'bongo-go mail dangling' first to see what it will do.",
	RunE: runMailPurgeDangling,
}

func init() {
	mailCmd.PersistentFlags().StringVar(&mailHTTPAddr, "http-addr", "127.0.0.1:8081", "address of the local node's control listener (server.control_addr)")
	mailCmd.PersistentFlags().StringVar(&mailSessionSecret, "session-secret", "", "web.session_secret of the node, required to authenticate the internal request (falls back to BONGO_WEB_SESSION_SECRET)")
	mailPurgeDanglingCmd.Flags().BoolVar(&mailPurgeConfirm, "confirm", false, "actually delete the listed metadata (required; the deletion cannot be undone)")

	mailCmd.AddCommand(mailDanglingCmd)
	mailCmd.AddCommand(mailPurgeDanglingCmd)
	rootCmd.AddCommand(mailCmd)
}

func mailRequest(method, url string) (*http.Response, error) {
	secret := mailSessionSecret
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
		return nil, fmt.Errorf("connecting to %s: %w", mailHTTPAddr, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		if err := resp.Body.Close(); err != nil {
			return nil, fmt.Errorf("closing response body: %w", err)
		}
		return nil, fmt.Errorf("server returned 401 Unauthorized: pass --session-secret (or set BONGO_WEB_SESSION_SECRET) matching the node's web.session_secret")
	}
	return resp, nil
}

// mailRequestJSON performs the request and decodes a successful JSON body into
// out.
func mailRequestJSON(method, path string, out any) error {
	url := fmt.Sprintf("http://%s%s", mailHTTPAddr, path)
	resp, err := mailRequest(method, url)
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
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

type danglingMailboxRef struct {
	Mailbox string `json:"Mailbox"`
	UID     uint32 `json:"UID"`
}

type danglingMailRecord struct {
	UserID             string               `json:"UserID"`
	GUID               string               `json:"GUID"`
	MissingHash        string               `json:"MissingHash"`
	Subject            string               `json:"Subject"`
	From               string               `json:"From"`
	ReceivedAt         int64                `json:"ReceivedAt"`
	UserExists         bool                 `json:"UserExists"`
	Mailboxes          []danglingMailboxRef `json:"Mailboxes"`
	MissingAttachments []string             `json:"MissingAttachments"`
}

type danglingMailResponse struct {
	Count    int                  `json:"count"`
	Dangling []danglingMailRecord `json:"dangling"`
}

// printDanglingRecords renders one block per message. This is what an operator
// decides from, so it shows the identifying headers alongside the mailbox
// entries: entries present means the message is live in a mailbox and shows as
// unopenable, none means the record is invisible debris.
func printDanglingRecords(records []danglingMailRecord) {
	for _, rec := range records {
		received := "unknown"
		if rec.ReceivedAt > 0 {
			received = time.Unix(rec.ReceivedAt, 0).UTC().Format(time.RFC3339)
		}
		subject := rec.Subject
		if subject == "" {
			subject = "(no subject)"
		}
		sender := rec.From
		if sender == "" {
			sender = "(no sender)"
		}

		fmt.Printf("\n  user:     %s", rec.UserID)
		if !rec.UserExists {
			fmt.Print("  [account no longer exists]")
		}
		fmt.Printf("\n  guid:     %s\n", rec.GUID)
		fmt.Printf("  subject:  %s\n", subject)
		fmt.Printf("  from:     %s\n", sender)
		fmt.Printf("  received: %s\n", received)
		fmt.Printf("  missing:  %s (raw message body)\n", rec.MissingHash)
		for _, h := range rec.MissingAttachments {
			fmt.Printf("            %s (attachment)\n", h)
		}
		if len(rec.Mailboxes) == 0 {
			fmt.Println("  mailbox:  none — no mailbox entry references this metadata")
			continue
		}
		for i, ref := range rec.Mailboxes {
			label := "  mailbox: "
			if i > 0 {
				label = "           "
			}
			fmt.Printf("%s %s uid %d\n", label, ref.Mailbox, ref.UID)
		}
	}
}

func runMailDangling(cmd *cobra.Command, args []string) error {
	var out danglingMailResponse
	if err := mailRequestJSON(http.MethodGet, "/internal/mail/dangling", &out); err != nil {
		return err
	}
	if out.Count == 0 {
		fmt.Println("No dangling mail metadata: every message body blob is present.")
		return nil
	}

	fmt.Printf("%d message(s) whose body blob is missing:\n", out.Count)
	printDanglingRecords(out.Dangling)
	fmt.Println("\nThese bodies are unrecoverable. Each record listed with a mailbox entry is")
	fmt.Println("visible to the user and fails to open; each with none is invisible debris.")
	fmt.Println("Run 'bongo-go mail purge-dangling --confirm' to remove the metadata and the")
	fmt.Println("mailbox entries that reference it.")
	return nil
}

func runMailPurgeDangling(cmd *cobra.Command, args []string) error {
	// Always report first, whether or not --confirm was given: the operator
	// sees exactly what is about to be destroyed, and a run without --confirm
	// is a dry run rather than an error with no information in it.
	var found danglingMailResponse
	if err := mailRequestJSON(http.MethodGet, "/internal/mail/dangling", &found); err != nil {
		return err
	}
	if found.Count == 0 {
		fmt.Println("No dangling mail metadata to remove.")
		return nil
	}

	fmt.Printf("%d message(s) whose body blob is missing:\n", found.Count)
	printDanglingRecords(found.Dangling)

	if !mailPurgeConfirm {
		fmt.Println("\nNothing was deleted. The message bodies are already gone and deleting this")
		fmt.Println("metadata cannot be undone, so re-run with --confirm to proceed.")
		return nil
	}

	var out struct {
		Found   int      `json:"found"`
		Count   int      `json:"count"`
		Deleted []string `json:"deleted"`
		Skipped []string `json:"skipped"`
	}
	if err := mailRequestJSON(http.MethodPost, "/internal/mail/purge-dangling", &out); err != nil {
		return err
	}

	fmt.Printf("\nRemoved metadata for %d message(s):\n", out.Count)
	for _, guid := range out.Deleted {
		fmt.Printf("  %s\n", guid)
	}
	if len(out.Skipped) > 0 {
		fmt.Printf("Skipped %d record(s) whose body blob was present again by the time the\n", len(out.Skipped))
		fmt.Println("delete ran, so the message is readable and was left alone:")
		for _, id := range out.Skipped {
			fmt.Printf("  %s\n", id)
		}
	}
	return nil
}
