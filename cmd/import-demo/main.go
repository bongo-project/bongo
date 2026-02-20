// Command import-demo imports demo email files into a running bongo-go instance via SMTP.
package main

import (
	"bufio"
	"fmt"
	"log"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	mailDir := os.Args[1]
	smtpAddr := "127.0.0.1:2525"
	recipient := "admin@localhost"

	if len(os.Args) > 2 {
		smtpAddr = os.Args[2]
	}
	if len(os.Args) > 3 {
		recipient = os.Args[3]
	}

	entries, err := os.ReadDir(mailDir)
	if err != nil {
		log.Fatalf("reading dir: %v", err)
	}

	var imported, failed int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(mailDir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Printf("SKIP %s: %v", entry.Name(), err)
			failed++
			continue
		}

		msg := cleanMessage(string(raw), recipient)
		from := extractFrom(msg)
		if from == "" {
			from = "unknown@localhost"
		}

		err = smtp.SendMail(smtpAddr, nil, from, []string{recipient}, []byte(msg))
		if err != nil {
			log.Printf("FAIL %s: %v", entry.Name(), err)
			failed++
			continue
		}

		log.Printf("OK   %s (from: %s)", entry.Name(), from)
		imported++
	}

	fmt.Printf("\nImported %d emails (%d failed)\n", imported, failed)
}

// cleanMessage strips the mbox "From " line and replaces @NAME@/@ADDRESS@ placeholders.
func cleanMessage(raw, recipient string) string {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	var lines []string
	first := true
	for scanner.Scan() {
		line := scanner.Text()
		// Skip mbox "From " envelope line (only first line).
		if first {
			first = false
			if strings.HasPrefix(line, "From ") && !strings.HasPrefix(line, "From:") {
				continue
			}
		}
		lines = append(lines, line)
	}

	msg := strings.Join(lines, "\r\n")

	// Replace placeholders used in some demo emails.
	msg = strings.ReplaceAll(msg, "@NAME@", "Demo User")
	msg = strings.ReplaceAll(msg, "@ADDRESS@", recipient)

	return msg
}

// extractFrom pulls the email address from the From: header.
func extractFrom(msg string) string {
	scanner := bufio.NewScanner(strings.NewReader(msg))
	inHeader := true
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line == "\r" {
			inHeader = false
		}
		if !inHeader {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "from:") {
			addr := line[5:]
			addr = strings.TrimSpace(addr)
			// Extract email from "Name <email>" format.
			if idx := strings.Index(addr, "<"); idx >= 0 {
				end := strings.Index(addr, ">")
				if end > idx {
					return addr[idx+1 : end]
				}
			}
			// Bare email address.
			return addr
		}
	}
	return ""
}
