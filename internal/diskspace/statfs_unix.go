//go:build linux || darwin

package diskspace

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// FreeBytes returns the space available to unprivileged writers on the
// filesystem holding dir. It uses Bavail, not Bfree: blocks reserved for root
// are not free for the server's purposes.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	// Bsize is int64 on linux and uint32 on darwin, hence the conversions.
	return uint64(st.Bavail) * uint64(st.Bsize), nil // #nosec G115 -- both fields are non-negative
}
