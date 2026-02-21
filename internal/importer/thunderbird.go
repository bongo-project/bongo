package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ThunderbirdSource reads messages from Thunderbird's local folder structure.
// Thunderbird stores mbox files (no extension) alongside .msf summary files.
// Subdirectories with a .sbd suffix contain nested folders.
type ThunderbirdSource struct {
	// folders maps IMAP-style folder name -> mbox file path
	folders map[string]string
}

// DiscoverThunderbirdProfiles returns all Thunderbird profile directories found
// on the local system.
func DiscoverThunderbirdProfiles() ([]string, error) {
	var roots []string

	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		roots = append(roots, filepath.Join(home, "Library", "Thunderbird", "Profiles"))
	case "linux":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		roots = append(roots, filepath.Join(home, ".thunderbird"))
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData != "" {
			roots = append(roots, filepath.Join(appData, "Thunderbird", "Profiles"))
		}
	}

	var profiles []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading %s: %w", root, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				profiles = append(profiles, filepath.Join(root, e.Name()))
			}
		}
	}

	if len(profiles) == 0 {
		return nil, fmt.Errorf("no Thunderbird profiles found")
	}
	return profiles, nil
}

// FindThunderbirdLocalFolders finds the "Mail/Local Folders" directory within
// a Thunderbird profile. Returns the path or an error if not found.
func FindThunderbirdLocalFolders(profileDir string) (string, error) {
	localFolders := filepath.Join(profileDir, "Mail", "Local Folders")
	info, err := os.Stat(localFolders)
	if err != nil {
		return "", fmt.Errorf("local folders not found at %s: %w", localFolders, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", localFolders)
	}
	return localFolders, nil
}

// NewThunderbirdSource creates a source from a Thunderbird local folders directory.
// It scans the directory tree for mbox files and maps them to IMAP folder names.
func NewThunderbirdSource(localFoldersDir string) (*ThunderbirdSource, error) {
	folders := make(map[string]string)
	if err := scanThunderbirdDir(localFoldersDir, "", folders); err != nil {
		return nil, err
	}
	if len(folders) == 0 {
		return nil, fmt.Errorf("no mbox files found in %s", localFoldersDir)
	}
	return &ThunderbirdSource{folders: folders}, nil
}

// scanThunderbirdDir recursively scans a Thunderbird directory for mbox files.
// Thunderbird convention:
//   - Files without extension (and not .msf) are mbox files
//   - Directories ending in .sbd contain sub-folders
//   - The mbox file name becomes the folder name
//   - Nested folders use "/" separator (e.g., "Work/Projects")
func scanThunderbirdDir(dir, prefix string, folders map[string]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		name := entry.Name()

		if entry.IsDir() {
			// .sbd directories contain sub-folders
			if strings.HasSuffix(name, ".sbd") {
				subPrefix := strings.TrimSuffix(name, ".sbd")
				if prefix != "" {
					subPrefix = prefix + "/" + subPrefix
				}
				if err := scanThunderbirdDir(filepath.Join(dir, name), subPrefix, folders); err != nil {
					return err
				}
			}
			continue
		}

		// Skip summary files and other metadata
		if strings.HasSuffix(name, ".msf") ||
			strings.HasSuffix(name, ".dat") ||
			strings.HasSuffix(name, ".html") ||
			name == "msgFilterRules.dat" ||
			name == "filterlog.html" {
			continue
		}

		// The file is likely an mbox. Map its name to an IMAP folder.
		folderName := thunderbirdToIMAPName(name, prefix)
		folders[folderName] = filepath.Join(dir, name)
	}

	return nil
}

// thunderbirdToIMAPName converts a Thunderbird mbox filename to an IMAP folder name.
func thunderbirdToIMAPName(filename, prefix string) string {
	name := filename

	// Map well-known Thunderbird names to standard IMAP names
	switch strings.ToLower(name) {
	case "inbox":
		name = "INBOX"
	case "sent":
		name = "Sent"
	case "drafts":
		name = "Drafts"
	case "trash":
		name = "Trash"
	case "junk":
		name = "Junk"
	case "archives":
		name = "Archive"
	}

	if prefix != "" {
		return prefix + "/" + name
	}
	return name
}

func (s *ThunderbirdSource) Folders() ([]string, error) {
	var names []string
	for name := range s.folders {
		names = append(names, name)
	}
	return names, nil
}

func (s *ThunderbirdSource) Messages(folder string) (<-chan Message, <-chan error) {
	path, ok := s.folders[folder]
	if !ok {
		msgCh := make(chan Message)
		errCh := make(chan error, 1)
		errCh <- fmt.Errorf("unknown folder %q", folder)
		close(msgCh)
		close(errCh)
		return msgCh, errCh
	}

	// Delegate to MboxSource for actual parsing
	mbox := NewMboxSource([]string{path}, folder)
	return mbox.Messages(folder)
}

func (s *ThunderbirdSource) Close() error { return nil }
