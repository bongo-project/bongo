//go:build !linux && !darwin

package diskspace

import "errors"

// FreeBytes is unsupported on this platform; the monitor treats the error as
// "not low" (fail open).
func FreeBytes(string) (uint64, error) {
	return 0, errors.New("free-space sampling is not supported on this platform")
}
