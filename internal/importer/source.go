package importer

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// Message represents a single email message to import.
type Message struct {
	Raw          []byte
	Flags        []imap.Flag
	InternalDate time.Time
	Folder       string
	// SourceID is an opaque identifier for resume tracking (e.g. UID, filename).
	SourceID string
}

// MessageSource provides messages from a source system.
type MessageSource interface {
	// Folders returns the list of source folders.
	Folders() ([]string, error)
	// Messages returns a channel of messages for a given folder.
	// The channel is closed when all messages have been sent or ctx is cancelled.
	Messages(folder string) (<-chan Message, <-chan error)
	// Close releases any resources.
	Close() error
}

// IMAPSource reads messages from a remote IMAP server.
type IMAPSource struct {
	client *imapclient.Client
}

// NewIMAPSource connects to an IMAP server and logs in.
func NewIMAPSource(host string, port int, useTLS bool, user, pass string) (*IMAPSource, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	var c *imapclient.Client
	var err error

	if useTLS {
		c, err = imapclient.DialTLS(addr, nil)
	} else {
		c, err = imapclient.DialInsecure(addr, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", addr, err)
	}

	loginCmd := c.Login(user, pass)
	if err := loginCmd.Wait(); err != nil {
		c.Close()
		return nil, fmt.Errorf("login failed: %w", err)
	}

	return &IMAPSource{client: c}, nil
}

func (s *IMAPSource) Folders() ([]string, error) {
	listCmd := s.client.List("", "*", nil)
	var folders []string
	for {
		mbox := listCmd.Next()
		if mbox == nil {
			break
		}
		folders = append(folders, mbox.Mailbox)
	}
	if err := listCmd.Close(); err != nil {
		return nil, fmt.Errorf("listing folders: %w", err)
	}
	return folders, nil
}

func (s *IMAPSource) Messages(folder string) (<-chan Message, <-chan error) {
	msgCh := make(chan Message, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)

		selCmd := s.client.Select(folder, nil)
		selData, err := selCmd.Wait()
		if err != nil {
			errCh <- fmt.Errorf("selecting %q: %w", folder, err)
			return
		}

		if selData.NumMessages == 0 {
			return
		}

		// Fetch all messages by sequence number.
		seqSet := imap.SeqSet{}
		seqSet.AddRange(1, selData.NumMessages)

		fetchOpts := &imap.FetchOptions{
			UID:          true,
			Flags:        true,
			InternalDate: true,
			BodySection:  []*imap.FetchItemBodySection{{Peek: true}},
		}
		fetchCmd := s.client.Fetch(seqSet, fetchOpts)
		for {
			msgData := fetchCmd.Next()
			if msgData == nil {
				break
			}

			var msg Message
			msg.Folder = folder

			for {
				item := msgData.Next()
				if item == nil {
					break
				}
				switch data := item.(type) {
				case imapclient.FetchItemDataUID:
					msg.SourceID = fmt.Sprintf("%d", data.UID)
				case imapclient.FetchItemDataFlags:
					msg.Flags = data.Flags
				case imapclient.FetchItemDataInternalDate:
					msg.InternalDate = data.Time
				case imapclient.FetchItemDataBodySection:
					raw, readErr := io.ReadAll(data.Literal)
					if readErr != nil {
						errCh <- fmt.Errorf("reading message body: %w", readErr)
						return
					}
					msg.Raw = raw
				}
			}

			if len(msg.Raw) > 0 {
				msgCh <- msg
			}
		}

		if err := fetchCmd.Close(); err != nil {
			errCh <- fmt.Errorf("fetching messages: %w", err)
		}
	}()

	return msgCh, errCh
}

func (s *IMAPSource) Close() error {
	if s.client != nil {
		return s.client.Close()
	}
	return nil
}

// MboxSource reads messages from mbox files.
type MboxSource struct {
	files  []string
	folder string
}

// NewMboxSource creates a source from mbox file paths.
func NewMboxSource(files []string, folder string) *MboxSource {
	return &MboxSource{files: files, folder: folder}
}

func (s *MboxSource) Folders() ([]string, error) {
	return []string{s.folder}, nil
}

func (s *MboxSource) Messages(folder string) (<-chan Message, <-chan error) {
	msgCh := make(chan Message, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)

		for _, path := range s.files {
			f, err := os.Open(path)
			if err != nil {
				errCh <- fmt.Errorf("opening %s: %w", path, err)
				return
			}

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

			var lines []string
			msgIdx := 0

			flush := func() {
				if len(lines) == 0 {
					return
				}
				raw := strings.Join(lines, "\r\n")
				msg := Message{
					Raw:          []byte(raw),
					Folder:       folder,
					InternalDate: time.Now(),
					SourceID:     fmt.Sprintf("%s:%d", filepath.Base(path), msgIdx),
				}
				// Try to parse Date header for internal date.
				if t := parseDateHeader(raw); !t.IsZero() {
					msg.InternalDate = t
				}
				msgCh <- msg
				msgIdx++
				lines = nil
			}

			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "From ") && !strings.HasPrefix(line, "From:") {
					flush()
					continue
				}
				lines = append(lines, line)
			}
			flush()

			f.Close()

			if err := scanner.Err(); err != nil {
				errCh <- fmt.Errorf("scanning %s: %w", path, err)
				return
			}
		}
	}()

	return msgCh, errCh
}

func (s *MboxSource) Close() error { return nil }

// MaildirSource reads messages from a Maildir directory.
type MaildirSource struct {
	root string
}

// NewMaildirSource creates a source from a Maildir root directory.
func NewMaildirSource(root string) *MaildirSource {
	return &MaildirSource{root: root}
}

func (s *MaildirSource) Folders() ([]string, error) {
	folders := []string{"INBOX"}

	// Scan for Maildir++ subdirectories (.Sent, .Drafts, etc).
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("reading maildir root: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && entry.Name() != "." {
			// Convert Maildir++ convention: .Sent -> Sent
			name := strings.TrimPrefix(entry.Name(), ".")
			name = strings.ReplaceAll(name, ".", "/")
			folders = append(folders, name)
		}
	}

	return folders, nil
}

func (s *MaildirSource) Messages(folder string) (<-chan Message, <-chan error) {
	msgCh := make(chan Message, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)

		var dir string
		if folder == "INBOX" {
			dir = s.root
		} else {
			// Convert back to Maildir++ directory name.
			dotName := "." + strings.ReplaceAll(folder, "/", ".")
			dir = filepath.Join(s.root, dotName)
		}

		for _, subdir := range []string{"cur", "new"} {
			path := filepath.Join(dir, subdir)
			entries, err := os.ReadDir(path)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				errCh <- fmt.Errorf("reading %s: %w", path, err)
				return
			}

			for _, entry := range entries {
				if entry.IsDir() {
					continue
				}

				raw, err := os.ReadFile(filepath.Join(path, entry.Name()))
				if err != nil {
					errCh <- fmt.Errorf("reading %s: %w", entry.Name(), err)
					return
				}

				msg := Message{
					Raw:          raw,
					Folder:       folder,
					Flags:        maildirFlags(entry.Name()),
					InternalDate: time.Now(),
					SourceID:     entry.Name(),
				}

				// Messages in "new" are unseen by convention.
				if subdir == "new" {
					// Remove \Seen if present.
					var filtered []imap.Flag
					for _, f := range msg.Flags {
						if f != imap.FlagSeen {
							filtered = append(filtered, f)
						}
					}
					msg.Flags = filtered
				}

				if t := parseDateHeader(string(raw)); !t.IsZero() {
					msg.InternalDate = t
				}

				msgCh <- msg
			}
		}
	}()

	return msgCh, errCh
}

func (s *MaildirSource) Close() error { return nil }

// maildirFlags extracts IMAP flags from a Maildir filename.
// Format: unique:2,flags where flags are single characters.
func maildirFlags(filename string) []imap.Flag {
	parts := strings.SplitN(filename, ":2,", 2)
	if len(parts) != 2 {
		return nil
	}

	var flags []imap.Flag
	for _, c := range parts[1] {
		switch c {
		case 'S':
			flags = append(flags, imap.FlagSeen)
		case 'R':
			flags = append(flags, imap.FlagAnswered)
		case 'F':
			flags = append(flags, imap.FlagFlagged)
		case 'T':
			flags = append(flags, imap.FlagDeleted)
		case 'D':
			flags = append(flags, imap.FlagDraft)
		}
	}
	return flags
}

// EMLSource reads individual .eml files.
type EMLSource struct {
	files  []string
	folder string
}

// NewEMLSource creates a source from .eml file paths.
func NewEMLSource(files []string, folder string) *EMLSource {
	return &EMLSource{files: files, folder: folder}
}

func (s *EMLSource) Folders() ([]string, error) {
	return []string{s.folder}, nil
}

func (s *EMLSource) Messages(folder string) (<-chan Message, <-chan error) {
	msgCh := make(chan Message, 16)
	errCh := make(chan error, 1)

	go func() {
		defer close(msgCh)
		defer close(errCh)

		for _, path := range s.files {
			raw, err := os.ReadFile(path)
			if err != nil {
				errCh <- fmt.Errorf("reading %s: %w", path, err)
				return
			}

			msg := Message{
				Raw:          raw,
				Folder:       folder,
				InternalDate: time.Now(),
				SourceID:     filepath.Base(path),
			}

			if t := parseDateHeader(string(raw)); !t.IsZero() {
				msg.InternalDate = t
			}

			msgCh <- msg
		}
	}()

	return msgCh, errCh
}

func (s *EMLSource) Close() error { return nil }

// parseDateHeader extracts the Date header value and parses it.
func parseDateHeader(raw string) time.Time {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == "\r" {
			break // end of headers
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "date:") {
			dateStr := strings.TrimSpace(line[5:])
			// Try common date formats.
			for _, layout := range []string{
				time.RFC1123Z,
				"Mon, 2 Jan 2006 15:04:05 -0700",
				"Mon, 02 Jan 2006 15:04:05 -0700",
				"2 Jan 2006 15:04:05 -0700",
				time.RFC1123,
				time.RFC822Z,
				time.RFC822,
			} {
				if t, err := time.Parse(layout, dateStr); err == nil {
					return t
				}
			}
		}
	}
	return time.Time{}
}
