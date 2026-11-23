package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/bongo-project/bongo/internal/fetch/fetchx"
)

// uidChunk bounds how many UIDs one command names, so a command line stays a
// sane length however large the mailbox is.
const uidChunk = 500

// IMAPSession is one authenticated IMAP connection with one folder selected.
// It is not safe for concurrent use. Close it when done.
type IMAPSession struct {
	ctx         context.Context
	c           *imapclient.Client
	bc          *budgetConn
	stop        func() bool
	uidValidity uint32
	readOnly    bool
	selected    bool
	numMessages uint32
}

// DialIMAP connects, secures the connection and signs in. With SecurityTLS the
// connection is TLS from the first byte; with SecurityStartTLS the client
// upgrades before LOGIN and a failed upgrade (no STARTTLS, a NO, a handshake
// error, PREAUTH) is ErrTLS with no fallback to plaintext. Certificates are
// verified against p.Host. The ctx deadline is the whole session's deadline.
func (c *Client) DialIMAP(ctx context.Context, p Params) (*IMAPSession, error) {
	bc, err := c.dial(ctx, p)
	if err != nil {
		return nil, err
	}
	s := &IMAPSession{ctx: ctx, bc: bc}
	s.stop = watch(ctx, bc)

	// DebugWriter stays nil: it would write the LOGIN command, password
	// included.
	switch p.Security {
	case SecurityTLS:
		tc, err := c.handshake(ctx, bc, p, "imap")
		if err != nil {
			s.abort()
			return nil, err
		}
		s.c = imapclient.New(tc, &imapclient.Options{})
	case SecurityStartTLS:
		client, err := imapclient.NewStartTLS(bc, &imapclient.Options{TLSConfig: c.tlsConfig(p.Host, "imap")})
		if err != nil {
			s.abort()
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%w", fetchx.ErrTimeout)
			}
			if bc.left.Load() <= 0 {
				return nil, fmt.Errorf("%w", fetchx.ErrBudget)
			}
			return nil, fmt.Errorf("%w", fetchx.ErrTLS)
		}
		s.c = client
	default:
		s.abort()
		return nil, errors.New("fetch: unknown security mode")
	}

	if err := s.do(s.c.WaitGreeting); err != nil {
		s.abort()
		return nil, err
	}
	if err := s.login(p.Username, p.Password); err != nil {
		s.abort()
		return nil, err
	}
	return s, nil
}

func (s *IMAPSession) abort() {
	s.stop()
	if s.c != nil {
		_ = s.c.Close()
	}
	_ = s.bc.Close()
}

// do runs one protocol exchange with the idle deadline and the control budget
// armed, and maps its error to a sentinel. A panic out of the library or of
// the code in f becomes ErrProtocol: a hostile server must not be able to crash
// the worker.
func (s *IMAPSession) do(f func() error) (err error) {
	s.bc.beginControl()
	defer s.bc.setBusy(false)
	defer recoverProtocol(&err)
	return s.mapErr(f())
}

// recoverProtocol turns a panic into ErrProtocol. It is deferred with the
// address of the named error result.
func recoverProtocol(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%w", fetchx.ErrProtocol)
	}
}

// mapErr turns whatever the library returned into a sentinel that carries no
// server text.
func (s *IMAPSession) mapErr(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{fetchx.ErrTooLarge, fetchx.ErrAuth, fetchx.ErrFolder, fetchx.ErrTLS, fetchx.ErrNoUIDL} {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%w", sentinel)
		}
	}
	if s.ctx.Err() != nil {
		return fmt.Errorf("%w", fetchx.ErrTimeout)
	}
	if s.bc.spent() || errors.Is(err, fetchx.ErrBudget) {
		return fmt.Errorf("%w", fetchx.ErrBudget)
	}
	var ne net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return fmt.Errorf("%w", fetchx.ErrTimeout)
	}
	var ie *imap.Error
	if errors.As(err, &ie) {
		switch ie.Type {
		case imap.StatusResponseTypeBye:
			return fmt.Errorf("%w", fetchx.ErrTransient)
		}
		switch ie.Code {
		case imap.ResponseCodeUnavailable, imap.ResponseCodeServerBug, imap.ResponseCodeInUse, imap.ResponseCodeLimit:
			return fmt.Errorf("%w", fetchx.ErrTransient)
		}
		return fmt.Errorf("%w", fetchx.ErrProtocol)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%w", fetchx.ErrTransient)
	}
	return fmt.Errorf("%w", fetchx.ErrProtocol)
}

// login signs in. A NO with AUTHENTICATIONFAILED, or with no code, is ErrAuth
// (LOGINDISABLED after TLS is a setting the user must fix, so it is ErrAuth
// too); UNAVAILABLE, SERVERBUG, INUSE and a BYE are temporary.
func (s *IMAPSession) login(user, pass string) (rerr error) {
	s.bc.beginControl()
	defer s.bc.setBusy(false)
	defer recoverProtocol(&rerr)
	err := s.c.Login(user, pass).Wait()
	if err == nil {
		return nil
	}
	var ie *imap.Error
	if errors.As(err, &ie) && ie.Type == imap.StatusResponseTypeNo {
		switch ie.Code {
		case imap.ResponseCodeUnavailable, imap.ResponseCodeServerBug, imap.ResponseCodeInUse:
			return fmt.Errorf("%w", fetchx.ErrTransient)
		}
		return fmt.Errorf("%w", fetchx.ErrAuth)
	}
	return s.mapErr(err)
}

// Select opens folder. With readOnly the server is asked for EXAMINE, which
// cannot change a flag (leave-on-server accounts never touch the remote copy).
// A folder that cannot be opened is ErrFolder. It returns UIDVALIDITY.
func (s *IMAPSession) Select(folder string, readOnly bool) (uidValidity uint32, err error) {
	s.bc.beginControl()
	defer s.bc.setBusy(false)
	defer recoverProtocol(&err)
	data, err := s.c.Select(folder, &imap.SelectOptions{ReadOnly: readOnly}).Wait()
	if err != nil {
		var ie *imap.Error
		if errors.As(err, &ie) && ie.Type == imap.StatusResponseTypeNo {
			return 0, fmt.Errorf("%w", fetchx.ErrFolder)
		}
		return 0, s.mapErr(err)
	}
	s.uidValidity, s.readOnly, s.selected, s.numMessages = data.UIDValidity, readOnly, true, data.NumMessages
	return data.UIDValidity, nil
}

// UIDValidity is the selected folder's UIDVALIDITY.
func (s *IMAPSession) UIDValidity() uint32 { return s.uidValidity }

// seqChunk is how many messages one listing command names. The library keeps
// every answered sequence number of a command in a sorted set, which costs
// time quadratic in the number of entries when a server sends them out of
// order; a bounded chunk keeps the worst case small and the total linear.
const seqChunk = 5000

// UIDs lists every UID in the selected folder, ascending. It does not use UID
// SEARCH: the library folds a SEARCH answer into a number set, where one
// hostile "0" makes the set dynamic (AllUIDs then panics) and one ESEARCH range
// of a few bytes expands to billions. Instead it sends FETCH a:b (UID) for the
// EXISTS count the SELECT reported, chunk by chunk, reads each answer one
// message at a time. A folder with more than MaxRemoteMessages messages is a
// protocol error.
func (s *IMAPSession) UIDs() ([]uint32, error) {
	if s.numMessages > MaxRemoteMessages {
		return nil, fmt.Errorf("%w", fetchx.ErrProtocol)
	}
	var out []uint32
	for start := uint32(1); start <= s.numMessages; start += seqChunk {
		stop := min(start+seqChunk-1, s.numMessages)
		err := s.do(func() error {
			var set imap.SeqSet
			set.AddRange(start, stop)
			cmd := s.c.Fetch(set, &imap.FetchOptions{UID: true})
			for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
				// Exactly one UID per message: the first. A server that
				// repeats the item in one answer must not add entries.
				seen := false
				for item := msg.Next(); item != nil; item = msg.Next() {
					if u, ok := item.(imapclient.FetchItemDataUID); ok && !seen {
						seen = true
						if u.UID != 0 {
							out = append(out, uint32(u.UID))
						}
					}
				}
			}
			if err := cmd.Close(); err != nil {
				return err
			}
			// The library delivers each requested sequence number once and we
			// keep one UID per message, so this cannot trip; it is the check
			// that the listing never outgrows what the folder reported.
			if int64(len(out)) > int64(stop) {
				return fetchx.ErrProtocol
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Sizes returns the RFC822.SIZE the server reports for each UID. The server may
// lie; FetchBody enforces the real limit. The answer is read item by item and
// anything that was not asked for (a BODY[] literal, say) is drained, never
// buffered, and counts against the control budget.
func (s *IMAPSession) Sizes(uids []uint32) (map[uint32]int64, error) {
	if len(uids) > MaxRemoteMessages {
		return nil, fmt.Errorf("%w", fetchx.ErrProtocol)
	}
	out := make(map[uint32]int64, min(len(uids), uidChunk))
	for chunk := range slices.Chunk(uids, uidChunk) {
		err := s.do(func() error {
			cmd := s.c.Fetch(uidSet(chunk), &imap.FetchOptions{UID: true, RFC822Size: true})
			for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
				var uid imap.UID
				var size int64 = -1
				for item := msg.Next(); item != nil; item = msg.Next() {
					switch it := item.(type) {
					case imapclient.FetchItemDataUID:
						uid = it.UID
					case imapclient.FetchItemDataRFC822Size:
						size = it.Size
					}
				}
				if uid != 0 && size >= 0 && len(out) < len(uids)+uidChunk {
					out[uint32(uid)] = size
				}
			}
			return cmd.Close()
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// FetchBody fetches one message with BODY.PEEK[] (which sets no \Seen) and
// hands its bytes to fn. The reader fails with ErrTooLarge once the message
// passes max bytes, whatever the server said RFC822.SIZE was; on that overflow
// the connection is closed, since draining an unbounded literal is not safe,
// and the session is finished. fn's own error is returned unchanged.
func (s *IMAPSession) FetchBody(uid uint32, max int64, fn func(io.Reader) error) (rerr error) {
	s.bc.beginUnbounded()
	defer s.bc.setBusy(false)
	defer recoverProtocol(&rerr)

	cmd := s.c.Fetch(uidSet([]uint32{uid}), &imap.FetchOptions{
		UID:         true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	})
	var fnErr error
	got := false
	for msg := cmd.Next(); msg != nil; msg = cmd.Next() {
		for item := msg.Next(); item != nil; item = msg.Next() {
			body, ok := item.(imapclient.FetchItemDataBodySection)
			if !ok || body.Literal == nil || got {
				continue
			}
			got = true
			// One limit for fn and for the drain: what fn read counts toward
			// the same max as what it left.
			lr := fetchx.LimitReader(body.Literal, max)
			fnErr = fn(lr)
			if fnErr == nil {
				_, fnErr = io.Copy(io.Discard, lr)
			}
			if fnErr != nil {
				// The literal is half read; the stream cannot be resynchronised
				// cheaply. Close and report.
				_ = s.bc.Close()
			}
		}
	}
	closeErr := cmd.Close()
	if fnErr != nil {
		if errors.Is(fnErr, fetchx.ErrTooLarge) {
			return fmt.Errorf("%w", fetchx.ErrTooLarge)
		}
		if s.ctx.Err() != nil || s.bc.spent() || isNetErr(fnErr) {
			return s.mapErr(fnErr)
		}
		return fnErr
	}
	if closeErr != nil {
		return s.mapErr(closeErr)
	}
	if !got {
		return fmt.Errorf("%w", fetchx.ErrProtocol)
	}
	return nil
}

func isNetErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed)
}

// DeleteUIDs flags the messages \Deleted and, only when the server advertises
// UIDPLUS (or IMAP4rev2), expunges exactly those UIDs. Without UIDPLUS they are
// left flagged: a plain EXPUNGE or CLOSE would also remove messages the user
// flagged elsewhere. The result says whether the messages were expunged. The
// session must have been opened read-write.
func (s *IMAPSession) DeleteUIDs(uids []uint32) (expunged bool, err error) {
	if s.readOnly || !s.selected {
		return false, errors.New("fetch: folder not open for writing")
	}
	if len(uids) == 0 {
		return false, nil
	}
	for chunk := range slices.Chunk(uids, uidChunk) {
		err := s.do(func() error {
			return s.c.Store(uidSet(chunk), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		})
		if err != nil {
			return false, err
		}
	}
	s.bc.beginControl()
	uidPlus := s.c.Caps().Has(imap.CapUIDPlus)
	s.bc.setBusy(false)
	if !uidPlus {
		return false, nil
	}
	for chunk := range slices.Chunk(uids, uidChunk) {
		err := s.do(func() error { return s.c.UIDExpunge(uidSet(chunk)).Close() })
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

func uidSet(uids []uint32) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	return set
}

// Close signs out and closes the connection. LOGOUT gets 5 seconds, then the
// connection is closed regardless: go-imap's Close can wait on a reader stuck
// mid-response.
func (s *IMAPSession) Close() {
	defer s.stop()
	s.bc.beginControl()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.c.Logout().Wait()
		_ = s.c.Close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.bc.Close()
		<-done
	}
	_ = s.bc.Close()
}
