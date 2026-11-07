//go:build !unix

package main

import (
	"errors"
	"time"
)

// processCPU is unavailable off unix, so a run there cannot detect CPU
// saturation and says so in its summary.
func processCPU() (time.Duration, error) {
	return 0, errors.New("process CPU measurement needs a unix system")
}
