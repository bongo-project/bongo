//go:build linux

package main

import "syscall"

// ipBindAddressNoPort is IP_BIND_ADDRESS_NO_PORT (Linux 4.2): bind the
// address now, choose the port at connect time.
const ipBindAddressNoPort = 24

// bindWithoutPort is best effort: an older kernel or a socket that refuses the
// option only loses the larger port space, so the error is dropped.
func bindWithoutPort(_, _ string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipBindAddressNoPort, 1)
	})
}
