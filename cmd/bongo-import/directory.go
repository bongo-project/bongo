package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bongo-go/bongo-go/internal/importer"
)

var (
	dirWebURL      string
	dirMailOnly    bool
	dirContactOnly bool
	dirCalOnly     bool
	dirYes         bool
)

// discovery holds classified files found during directory scan.
type discovery struct {
	mboxFiles   []string            // .mbox / .mbx files
	emlByFolder map[string][]string // parent dir name -> []eml paths
	maildirs    []string            // Maildir root paths
	vcfFiles    []string            // .vcf files
	icsFiles    []string            // .ics files
}

var directoryCmd = &cobra.Command{
	Use:   "directory <path>",
	Short: "Recursively scan a directory and import mail, contacts, and calendar files",
	Long: `Walks a directory tree and classifies files by extension:

  .mbox / .mbx  -> mail (IMAP APPEND via mbox parser)
  .eml          -> mail (IMAP APPEND, grouped by parent directory)
  Maildir/      -> mail (IMAP APPEND, auto-detected by cur/ or new/ subdirs)
  .vcf          -> contacts (HTTP POST to web API)
  .ics          -> calendar (HTTP POST to web API)

Mail is imported via the standard IMAP APPEND pipeline (--dst-* flags).
Contacts and calendar files are uploaded to the bongo-go web API (--web-url flag).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if dstUser == "" || dstPass == "" {
			return fmt.Errorf("--dst-user and --dst-pass are required")
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		rootDir := args[0]

		// Discover files.
		disc, err := scanDirectory(rootDir)
		if err != nil {
			return fmt.Errorf("scanning directory: %w", err)
		}

		// Apply type filters.
		if dirMailOnly {
			disc.vcfFiles = nil
			disc.icsFiles = nil
		}
		if dirContactOnly {
			disc.mboxFiles = nil
			disc.emlByFolder = nil
			disc.maildirs = nil
			disc.icsFiles = nil
		}
		if dirCalOnly {
			disc.mboxFiles = nil
			disc.emlByFolder = nil
			disc.maildirs = nil
			disc.vcfFiles = nil
		}

		// Count EML files across all folders.
		emlCount := 0
		for _, files := range disc.emlByFolder {
			emlCount += len(files)
		}

		if len(disc.mboxFiles) == 0 && emlCount == 0 && len(disc.maildirs) == 0 &&
			len(disc.vcfFiles) == 0 && len(disc.icsFiles) == 0 {
			logger.Info("no importable files found", "path", rootDir)
			return nil
		}

		// Print summary and confirm.
		fmt.Fprintf(os.Stderr, "\nFound in %s:\n", rootDir)
		if len(disc.mboxFiles) > 0 || emlCount > 0 || len(disc.maildirs) > 0 {
			parts := []string{}
			if len(disc.mboxFiles) > 0 {
				parts = append(parts, fmt.Sprintf("%d .mbox files", len(disc.mboxFiles)))
			}
			if emlCount > 0 {
				parts = append(parts, fmt.Sprintf("%d .eml files", emlCount))
			}
			if len(disc.maildirs) > 0 {
				parts = append(parts, fmt.Sprintf("%d Maildir directories", len(disc.maildirs)))
			}
			fmt.Fprintf(os.Stderr, "  Mail:     %s\n", strings.Join(parts, ", "))
		}
		if len(disc.vcfFiles) > 0 {
			fmt.Fprintf(os.Stderr, "  Contacts: %d .vcf files\n", len(disc.vcfFiles))
		}
		if len(disc.icsFiles) > 0 {
			fmt.Fprintf(os.Stderr, "  Calendar: %d .ics files\n", len(disc.icsFiles))
		}

		if !dirYes {
			fmt.Fprint(os.Stderr, "Proceed? [Y/n] ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(strings.ToLower(line))
			if line != "" && line != "y" && line != "yes" {
				return fmt.Errorf("aborted by user")
			}
		}

		ctx := context.Background()

		// Import contacts first.
		if len(disc.vcfFiles) > 0 {
			logger.Info("importing contacts", "count", len(disc.vcfFiles))
			client, err := webLogin(dirWebURL, dstUser, dstPass)
			if err != nil {
				return fmt.Errorf("web login for contacts: %w", err)
			}
			for _, vcf := range disc.vcfFiles {
				if dryRun {
					logger.Info("dry-run: would import contact", "file", vcf)
					continue
				}
				if err := uploadFile(client, dirWebURL+"/contacts/import", "vcf", vcf); err != nil {
					return fmt.Errorf("importing contact %s: %w", vcf, err)
				}
				logger.Info("imported contact", "file", filepath.Base(vcf))
			}
		}

		// Import calendar.
		if len(disc.icsFiles) > 0 {
			logger.Info("importing calendar events", "count", len(disc.icsFiles))
			client, err := webLogin(dirWebURL, dstUser, dstPass)
			if err != nil {
				return fmt.Errorf("web login for calendar: %w", err)
			}
			for _, ics := range disc.icsFiles {
				if dryRun {
					logger.Info("dry-run: would import calendar", "file", ics)
					continue
				}
				if err := uploadFile(client, dirWebURL+"/calendar/import", "ics", ics); err != nil {
					return fmt.Errorf("importing calendar %s: %w", ics, err)
				}
				logger.Info("imported calendar", "file", filepath.Base(ics))
			}
		}

		// Import mail.
		hasMail := len(disc.mboxFiles) > 0 || emlCount > 0 || len(disc.maildirs) > 0
		if hasMail {
			state, err := importer.NewState(stateFile)
			if err != nil {
				return err
			}

			im := importer.NewImporter(importer.Config{
				Host: dstHost,
				Port: dstPort,
				TLS:  dstTLS,
				User: dstUser,
				Pass: dstPass,
			}, state, logger)
			im.SetDryRun(dryRun)

			// Import mbox files.
			for _, mboxPath := range disc.mboxFiles {
				folder := folderFromPath(mboxPath, rootDir)
				logger.Info("importing mbox", "file", mboxPath, "folder", folder)
				src := importer.NewMboxSource([]string{mboxPath}, folder)
				if err := im.Import(ctx, src); err != nil {
					return fmt.Errorf("importing mbox %s: %w", mboxPath, errors.Join(err, src.Close()))
				}
				if err := src.Close(); err != nil {
					return fmt.Errorf("closing mbox source %s: %w", mboxPath, err)
				}
			}

			// Import EML files grouped by folder.
			for folder, files := range disc.emlByFolder {
				logger.Info("importing eml files", "folder", folder, "count", len(files))
				src := importer.NewEMLSource(files, folder)
				if err := im.Import(ctx, src); err != nil {
					return fmt.Errorf("importing eml folder %s: %w", folder, errors.Join(err, src.Close()))
				}
				if err := src.Close(); err != nil {
					return fmt.Errorf("closing eml source %s: %w", folder, err)
				}
			}

			// Import Maildir directories.
			for _, mdPath := range disc.maildirs {
				logger.Info("importing maildir", "path", mdPath)
				src := importer.NewMaildirSource(mdPath)
				if err := im.Import(ctx, src); err != nil {
					return fmt.Errorf("importing maildir %s: %w", mdPath, errors.Join(err, src.Close()))
				}
				if err := src.Close(); err != nil {
					return fmt.Errorf("closing maildir source %s: %w", mdPath, err)
				}
			}
		}

		logger.Info("directory import complete")
		return nil
	},
}

func init() {
	f := directoryCmd.Flags()
	f.StringVar(&dirWebURL, "web-url", "https://localhost:8080", "Web API base URL for contacts/calendar import")
	f.BoolVar(&dirMailOnly, "mail-only", false, "Import only mail files")
	f.BoolVar(&dirContactOnly, "contacts-only", false, "Import only contact files")
	f.BoolVar(&dirCalOnly, "calendar-only", false, "Import only calendar files")
	f.BoolVar(&dirYes, "yes", false, "Skip confirmation prompt")
}

// scanDirectory walks the directory tree and classifies files.
func scanDirectory(root string) (*discovery, error) {
	disc := &discovery{
		emlByFolder: make(map[string][]string),
	}

	// Track directories we have already claimed as Maildir roots so we
	// don't also pick up individual .eml-like files inside them.
	maildirRoots := map[string]bool{}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Detect Maildir directories: a directory containing cur/ or new/.
		if info.IsDir() {
			if isMaildir(path) {
				disc.maildirs = append(disc.maildirs, path)
				maildirRoots[path] = true
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files inside already-claimed Maildir trees.
		for md := range maildirRoots {
			if strings.HasPrefix(path, md+string(filepath.Separator)) {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".mbox", ".mbx":
			disc.mboxFiles = append(disc.mboxFiles, path)
		case ".eml":
			folder := folderFromPath(path, root)
			disc.emlByFolder[folder] = append(disc.emlByFolder[folder], path)
		case ".vcf":
			disc.vcfFiles = append(disc.vcfFiles, path)
		case ".ics":
			disc.icsFiles = append(disc.icsFiles, path)
		}

		return nil
	})

	return disc, err
}

// isMaildir returns true if dir contains a "cur" or "new" subdirectory,
// which is the standard Maildir layout.
func isMaildir(dir string) bool {
	for _, sub := range []string{"cur", "new"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// folderFromPath derives a mail folder name from a file path relative to root.
// If the file is directly in root, returns "INBOX".
func folderFromPath(filePath, root string) string {
	rel, err := filepath.Rel(root, filepath.Dir(filePath))
	if err != nil || rel == "." {
		return "INBOX"
	}
	// Convert path separators to IMAP hierarchy separator.
	return strings.ReplaceAll(rel, string(filepath.Separator), "/")
}

// webLogin authenticates to the bongo-go web UI and returns an HTTP client
// with the session cookie set.
func webLogin(baseURL, user, pass string) (*http.Client, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- import tool supports local/self-signed Bongo web endpoints
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("creating cookie jar: %w", err)
	}

	client := &http.Client{
		Transport: transport,
		Jar:       jar,
	}

	form := url.Values{}
	form.Set("username", user)
	form.Set("password", pass)

	resp, err := client.PostForm(baseURL+"/login", form)
	if err != nil {
		return nil, fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return client, nil
}

// uploadFile sends a file as a multipart form upload.
func uploadFile(client *http.Client, endpoint, fieldName, filePath string) error {
	f, err := os.Open(filePath) // #nosec G304 -- import upload file path is selected by the operator
	if err != nil {
		return fmt.Errorf("opening %s: %w", filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("creating form file: %w", err)
	}

	if _, err := io.Copy(part, f); err != nil {
		return fmt.Errorf("copying file data: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("closing multipart writer: %w", err)
	}

	resp, err := client.Post(endpoint, writer.FormDataContentType(), &buf)
	if err != nil {
		return fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
