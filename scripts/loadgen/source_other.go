//go:build !linux

package main

import "syscall"

// bindWithoutPort does nothing off Linux, which has no such option.
func bindWithoutPort(_, _ string, _ syscall.RawConn) error { return nil }
