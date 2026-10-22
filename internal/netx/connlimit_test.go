package netx

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestPerIPLimiter(t *testing.T) {
	a := net.ParseIP("203.0.113.7")
	b := net.ParseIP("198.51.100.9")

	t.Run("refuses past the cap and release frees a slot", func(t *testing.T) {
		l := NewPerIPLimiter(3)
		for i := range 3 {
			if !l.Acquire(a) {
				t.Fatalf("acquire %d refused", i)
			}
		}
		if l.Acquire(a) {
			t.Fatal("fourth acquire granted")
		}
		l.Release(a)
		if !l.Acquire(a) {
			t.Fatal("acquire after release refused")
		}
	})

	t.Run("other IP unaffected", func(t *testing.T) {
		l := NewPerIPLimiter(1)
		if !l.Acquire(a) || l.Acquire(a) {
			t.Fatal("cap not enforced for a")
		}
		if !l.Acquire(b) {
			t.Fatal("b refused while a is full")
		}
	})

	t.Run("disabled and nil", func(t *testing.T) {
		for _, max := range []int{0, -1} {
			l := NewPerIPLimiter(max)
			for range 100 {
				if !l.Acquire(a) {
					t.Fatalf("max %d refused", max)
				}
			}
		}
		l := NewPerIPLimiter(1)
		for range 5 {
			if !l.Acquire(nil) {
				t.Fatal("nil ip refused")
			}
		}
		var nilLimiter *PerIPLimiter
		if !nilLimiter.Acquire(a) {
			t.Fatal("nil limiter refused")
		}
		nilLimiter.Release(a)
	})

	t.Run("entry deleted at zero and extra release harmless", func(t *testing.T) {
		l := NewPerIPLimiter(2)
		l.Acquire(a)
		l.Acquire(a)
		l.Release(a)
		if l.Len() != 1 {
			t.Fatalf("len = %d, want 1", l.Len())
		}
		l.Release(a)
		l.Release(a)
		if l.Len() != 0 {
			t.Fatalf("len = %d, want 0", l.Len())
		}
		for i := range 2 {
			if !l.Acquire(a) {
				t.Fatalf("acquire %d refused after extra release", i)
			}
		}
		if l.Acquire(a) {
			t.Fatal("cap exceeded after extra release")
		}
	})
}

func TestPerIPLimiterMax(t *testing.T) {
	var nilLimiter *PerIPLimiter
	if got := nilLimiter.Max(); got != 0 {
		t.Errorf("nil Max() = %d, want 0", got)
	}
	l := NewPerIPLimiter(7)
	if got := l.Max(); got != 7 {
		t.Errorf("Max() = %d, want 7", got)
	}
	l.SetMax(3)
	if got := l.Max(); got != 3 {
		t.Errorf("Max() after SetMax = %d, want 3", got)
	}
}

func TestLimitPerIPListener(t *testing.T) {
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	lim := NewPerIPLimiter(1)
	ln := LimitPerIP(raw, lim)

	var lastClient net.Conn
	accept := func() net.Conn {
		t.Helper()
		cl, err := net.Dial("tcp", raw.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cl.Close() })
		lastClient = cl
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	first := accept()
	if _, err := first.Write([]byte("x")); err != nil {
		t.Fatalf("first conn refused: %v", err)
	}

	second := accept()
	buf := make([]byte, 1)
	if _, err := second.Read(buf); !errors.Is(err, ErrTooManyConnections) {
		t.Fatalf("second Read err = %v, want ErrTooManyConnections", err)
	}
	// The refusal itself must close the socket, not rely on the caller.
	_ = lastClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := lastClient.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("client of refused conn read err = %v, want EOF", err)
	}
	if _, err := second.Write([]byte("x")); err == nil {
		t.Fatal("write on refused conn succeeded")
	}
	_ = second.Close()
	if lim.Len() != 1 {
		t.Fatalf("limiter holds %d IPs, want 1 (refused conn must not hold a slot)", lim.Len())
	}

	_ = first.Close()
	_ = first.Close()
	if lim.Len() != 0 {
		t.Fatalf("slot not released on Close, Len = %d", lim.Len())
	}

	third := accept()
	if _, err := third.Write([]byte("x")); err != nil {
		t.Fatalf("conn after release refused: %v", err)
	}
	_ = third.Close()
}
