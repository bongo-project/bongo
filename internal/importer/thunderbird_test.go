package importer

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func createMbox(t *testing.T, path string) {
	t.Helper()
	content := "From sender@example.com Mon Jan 1 00:00:00 2024\r\n" +
		"From: sender@example.com\r\n" +
		"To: recipient@example.com\r\n" +
		"Subject: Test message\r\n" +
		"Date: Mon, 01 Jan 2024 00:00:00 +0000\r\n" +
		"\r\n" +
		"Hello world\r\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestThunderbirdSourceDiscovery(t *testing.T) {
	// Create a mock Thunderbird local folders structure
	dir := t.TempDir()

	// Top-level mbox files
	createMbox(t, filepath.Join(dir, "Inbox"))
	createMbox(t, filepath.Join(dir, "Sent"))
	createMbox(t, filepath.Join(dir, "Drafts"))
	createMbox(t, filepath.Join(dir, "Trash"))

	// .msf files should be skipped
	os.WriteFile(filepath.Join(dir, "Inbox.msf"), []byte("summary"), 0644)

	// Nested folder: Work.sbd/Projects (becomes "Work/Projects")
	workDir := filepath.Join(dir, "Work.sbd")
	os.MkdirAll(workDir, 0755)
	createMbox(t, filepath.Join(dir, "Work"))
	createMbox(t, filepath.Join(workDir, "Projects"))
	createMbox(t, filepath.Join(workDir, "Personal"))

	src, err := NewThunderbirdSource(dir)
	if err != nil {
		t.Fatalf("NewThunderbirdSource: %v", err)
	}
	defer src.Close()

	folders, err := src.Folders()
	if err != nil {
		t.Fatalf("Folders: %v", err)
	}

	sort.Strings(folders)
	expected := []string{"Drafts", "INBOX", "Sent", "Trash", "Work", "Work/Personal", "Work/Projects"}

	if len(folders) != len(expected) {
		t.Fatalf("expected %d folders, got %d: %v", len(expected), len(folders), folders)
	}

	for i, name := range expected {
		if folders[i] != name {
			t.Errorf("folder %d: expected %q, got %q", i, name, folders[i])
		}
	}
}

func TestThunderbirdSourceMessages(t *testing.T) {
	dir := t.TempDir()
	createMbox(t, filepath.Join(dir, "Inbox"))

	src, err := NewThunderbirdSource(dir)
	if err != nil {
		t.Fatalf("NewThunderbirdSource: %v", err)
	}

	msgCh, errCh := src.Messages(context.Background(), "INBOX")
	var count int
	for range msgCh {
		count++
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Messages error: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 message, got %d", count)
	}
}

func TestThunderbirdNameMapping(t *testing.T) {
	tests := []struct {
		filename string
		prefix   string
		want     string
	}{
		{"Inbox", "", "INBOX"},
		{"Sent", "", "Sent"},
		{"Drafts", "", "Drafts"},
		{"Trash", "", "Trash"},
		{"Junk", "", "Junk"},
		{"Archives", "", "Archive"},
		{"Custom Folder", "", "Custom Folder"},
		{"Projects", "Work", "Work/Projects"},
		{"Inbox", "Backup", "Backup/INBOX"},
	}

	for _, tt := range tests {
		got := thunderbirdToIMAPName(tt.filename, tt.prefix)
		if got != tt.want {
			t.Errorf("thunderbirdToIMAPName(%q, %q) = %q, want %q", tt.filename, tt.prefix, got, tt.want)
		}
	}
}

func TestThunderbirdEmptyDir(t *testing.T) {
	dir := t.TempDir()
	_, err := NewThunderbirdSource(dir)
	if err == nil {
		t.Error("expected error for empty directory")
	}
}
