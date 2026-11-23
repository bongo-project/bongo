// Package fetchx holds what the remote-account clients share: the error
// sentinels and the size-limited reader. It exists so the POP3 client, which
// has no dependency on the rest of the fetcher, and the IMAP client report
// failures the same way.
//
// None of these errors carries server text. Servers echo user names and
// occasionally passwords, so what a server said is never stored, logged or
// shown.
package fetchx

import (
	"errors"
	"io"
)

var (
	// ErrAuth means the server refused the credentials. It counts toward the
	// five-failure pause.
	ErrAuth = errors.New("authentication refused")
	// ErrTLS means certificate verification, the handshake or STARTTLS failed.
	// There is never a fallback to plaintext.
	ErrTLS = errors.New("tls failed")
	// ErrFolder means the IMAP folder does not exist or cannot be opened.
	ErrFolder = errors.New("folder missing")
	// ErrNoUIDL means the POP3 server cannot list unique ids, so there is no
	// safe way to tell new mail from old.
	ErrNoUIDL = errors.New("server has no UIDL")
	// ErrProtocol means the server broke the protocol or a size or line limit.
	ErrProtocol = errors.New("protocol error")
	// ErrTransient means the server said "try later" (or hung up).
	ErrTransient = errors.New("temporary server failure")
	// ErrTooLarge means one message is larger than the limit. It is a skip for
	// that message, not a failure of the account.
	ErrTooLarge = errors.New("message too large")
	// ErrBudget means the session read more bytes than its budget allows.
	ErrBudget = errors.New("session byte budget spent")
	// ErrTimeout means the server went silent, or the account's run ran out of
	// time.
	ErrTimeout = errors.New("timed out")
	// ErrConnect means no connection could be made.
	ErrConnect = errors.New("connection failed")
)

// LimitReader returns a reader that yields at most max bytes of r and then
// fails with ErrTooLarge if r has more. Unlike io.LimitReader it never turns an
// overflow into a clean EOF, so a truncated message cannot pass for a whole
// one.
func LimitReader(r io.Reader, max int64) io.Reader {
	return &limitReader{r: r, left: max}
}

type limitReader struct {
	r    io.Reader
	left int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		// Probe for one more byte: EOF here means the data fitted exactly.
		var one [1]byte
		n, err := l.r.Read(one[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	return n, err
}
