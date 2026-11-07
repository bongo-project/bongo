package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path through a temporary file and a rename,
// with mode 0600, so a reader never sees half a file and nothing the tool
// writes is readable by other users.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".loadgen-out-*")
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("write output: %w", err))
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(fmt.Errorf("chmod output: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("sync output: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replace output: %w", err)
	}
	return nil
}
