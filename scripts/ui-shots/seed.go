package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
)

// seed populates the dev server: validate-server for the baseline users and
// mail, then testdata/demo imported into the admin mailbox, contacts and
// calendar. Everything the importer writes (its state file) stays in work.
func seed(ctx context.Context, repo, work string, s *server) error {
	env := cleanEnv(work)
	p := s.ports
	if err := run(ctx, work, env, filepath.Join(work, "validate-server"),
		"-plain-http", "-seed-only",
		"-host", "127.0.0.1",
		"-web-port", strconv.Itoa(p.web),
		"-smtp-port", strconv.Itoa(p.submission),
		"-imap-port", strconv.Itoa(p.imap),
		"-pop3-port", strconv.Itoa(p.pop3),
	); err != nil {
		return fmt.Errorf("seed baseline data: %w", err)
	}
	if err := run(ctx, work, env, filepath.Join(work, "bongo-import"),
		"directory", filepath.Join(repo, "testdata", "demo"), "--yes",
		"--dst-host", "127.0.0.1",
		"--dst-port", strconv.Itoa(p.imap),
		"--dst-user", adminUser,
		"--dst-pass", adminPass,
		"--web-url", p.origin(),
		"--state", filepath.Join(work, "import-state.json"),
	); err != nil {
		return fmt.Errorf("import demo data: %w", err)
	}
	return nil
}
