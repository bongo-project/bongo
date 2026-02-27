package importer

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/emersion/go-imap/v2"
)

// GmailFolderMap builds folder mappings and exclusion lists from IMAP
// special-use attributes (RFC 6154). This handles Gmail's locale-dependent
// folder names (e.g. [Gmail]/Sent Mail, [Google Mail]/Gesendete Nachrichten)
// by using the server-reported attributes instead of string matching.
//
// Returns:
//   - folderMap: source folder name -> destination folder name
//   - exclude: folder names to skip entirely
//   - lastFolders: folders that should be imported last (e.g. \All for dedup)
func GmailFolderMap(attrs map[string][]imap.MailboxAttr) (folderMap map[string]string, exclude []string, lastFolders []string) {
	folderMap = make(map[string]string)

	// Map from special-use attribute to desired destination name.
	attrMapping := map[imap.MailboxAttr]string{
		imap.MailboxAttrSent:    "Sent",
		imap.MailboxAttrDrafts:  "Drafts",
		imap.MailboxAttrTrash:   "Trash",
		imap.MailboxAttrJunk:    "Junk",
		imap.MailboxAttrFlagged: "Starred",
		imap.MailboxAttrAll:     "Archive",
	}

	// Attributes that cause a folder to be excluded entirely.
	excludeAttrs := map[imap.MailboxAttr]bool{
		imap.MailboxAttrImportant:   true,
		imap.MailboxAttrNoSelect:    true,
		imap.MailboxAttrNonExistent: true,
	}

	for folder, folderAttrs := range attrs {
		excluded := false
		for _, attr := range folderAttrs {
			if excludeAttrs[attr] {
				exclude = append(exclude, folder)
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}

		for _, attr := range folderAttrs {
			if dest, ok := attrMapping[attr]; ok {
				// Don't map if already named correctly.
				if folder != dest {
					folderMap[folder] = dest
				}
				// \All folders should be imported last for dedup.
				if attr == imap.MailboxAttrAll {
					lastFolders = append(lastFolders, folder)
				}
				break
			}
		}
	}

	return folderMap, exclude, lastFolders
}

// extractMessageID extracts the Message-ID header from raw RFC 5322 message bytes.
// Returns empty string if not found.
func extractMessageID(raw []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == "\r" {
			break // end of headers
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "message-id:") {
			return strings.TrimSpace(line[len("message-id:"):])
		}
	}
	return ""
}
