//go:build unix

package main

import (
	"fmt"
	"syscall"
	"time"
)

// processCPU returns the user plus system CPU time this process has used.
// Self-measurement is unix only (getrusage).
func processCPU() (time.Duration, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, fmt.Errorf("getrusage: %w", err)
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), nil
}
