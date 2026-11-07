package netx

import (
	"net"
	"sync/atomic"
)

// Gauge and Counter are the parts of a Prometheus gauge and counter the
// listeners use, declared here so netx does not import the metrics package.
type (
	Gauge interface {
		Inc()
		Dec()
	}
	Counter interface {
		Inc()
	}
)

// CountConns wraps ln so every accepted connection increments accepted and
// open, and closing it decrements open exactly once. Use it for listeners with
// no per-IP limiter; with one, use LimitPerIP's WithConnMetrics so refused
// connections are not counted.
func CountConns(ln net.Listener, open Gauge, accepted Counter) net.Listener {
	return &countListener{Listener: ln, open: open, accepted: accepted}
}

type countListener struct {
	net.Listener
	open     Gauge
	accepted Counter
}

func (l *countListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.accepted.Inc()
	l.open.Inc()
	return &countConn{Conn: c, open: l.open}, nil
}

type countConn struct {
	net.Conn
	open   Gauge
	closed atomic.Bool
}

// Close decrements the open gauge once however many times it is called: tls.Conn
// and the protocol libraries close more than once.
func (c *countConn) Close() error {
	err := c.Conn.Close()
	if c.closed.CompareAndSwap(false, true) {
		c.open.Dec()
	}
	return err
}
