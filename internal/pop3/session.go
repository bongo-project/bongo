package pop3

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/auth"
	"github.com/bongo-project/bongo/internal/authlimit"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// defaultIdleTimeout bounds how long a POP3 connection may sit without sending
// a complete command before it is dropped. RFC 1939 requires an autologout
// timer of at least 10 minutes; this also sheds idle/abandoned connections
// that would otherwise hold a goroutine and socket open indefinitely.
const defaultIdleTimeout = 10 * time.Minute

// popMsg represents a message in the POP3 session snapshot.
type popMsg struct {
	uid      uint32
	guid     string
	blobHash string
	size     int64
	meta     *bongov1.MailMeta // for reconstructing split messages at RETR/TOP
}

// authenticator is what POP3 needs of the auth backend: credentials checked
// with the profile they were checked against, and the live account state.
type authenticator interface {
	AuthenticateProfile(username, password string) (string, *bongov1.UserProfile, error)
	AccountState(userID string) (auth.AccountState, error)
}

var _ authenticator = (*auth.LocalAuth)(nil)

// session handles a single POP3 connection.
type session struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer

	auth        authenticator
	mailboxes   *store.MailboxStore
	pebble      *store.PebbleStore
	blobs       *store.BlobStore
	tlsCfg      *tls.Config
	isTLS       bool
	isDev       bool
	authLimiter *authlimit.Limiter
	logger      *slog.Logger

	// idleTimeout is the maximum time to wait for the next command before the
	// connection is dropped. Zero disables the deadline.
	idleTimeout time.Duration

	// Authorization state.
	username string

	// Transaction state.
	authenticated bool
	userID        string
	// epoch is the credential fingerprint recorded at PASS; a mismatch later
	// means the password was reset.
	epoch    string
	messages []popMsg // 0-indexed; POP3 message numbers are 1-based
	deleted  map[int]bool
}

func newSession(
	conn net.Conn,
	authenticator authenticator,
	mailboxes *store.MailboxStore,
	pebble *store.PebbleStore,
	blobs *store.BlobStore,
	tlsCfg *tls.Config,
	isTLS bool,
	isDev bool,
	authLimiter *authlimit.Limiter,
	logger *slog.Logger,
) *session {
	return &session{
		conn:        conn,
		reader:      bufio.NewReader(conn),
		writer:      bufio.NewWriter(conn),
		auth:        authenticator,
		mailboxes:   mailboxes,
		pebble:      pebble,
		blobs:       blobs,
		tlsCfg:      tlsCfg,
		isTLS:       isTLS,
		isDev:       isDev,
		authLimiter: authLimiter,
		logger:      logger,
		idleTimeout: defaultIdleTimeout,
		deleted:     make(map[int]bool),
	}
}

// clientIP returns the remote client's IP for auth-throttle keying. The POP3
// listeners are wrapped with the PROXY-protocol reader, so RemoteAddr reflects
// the real client behind the proxy when that proxy is listed in
// server.trusted_proxies; otherwise it is the TCP peer.
func (s *session) clientIP() string {
	if s.conn == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(s.conn.RemoteAddr().String()); err == nil {
		return host
	}
	return s.conn.RemoteAddr().String()
}

func (s *session) handle() {
	defer s.conn.Close()

	// Bound the greeting write too: a client that connects but never reads is
	// just as idle as one that never writes, and an unbounded write would hang
	// this goroutine and socket indefinitely.
	s.extendDeadline()
	s.respond("+OK bongo-go POP3 ready")

	for {
		// Drop the connection if the client goes idle between commands.
		s.extendDeadline()

		line, err := s.reader.ReadString('\n')
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				// RFC 1939 autologout: close the connection without entering
				// UPDATE state. No goodbye is sent (the client is gone, and a
				// write could block on an unread socket).
				s.logger.Debug("pop3: idle timeout, closing connection")
			}
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		cmd, args := s.parseCommand(line)
		cmd = strings.ToUpper(cmd)

		// A revoked account gets nothing further, and in particular its pending
		// DELE marks are not applied (no UPDATE state).
		// A store error that hides the account state is refused the same way,
		// with a temporary code, so access is never granted unverified.
		if s.authenticated {
			live, err := s.accountLive()
			if err != nil {
				s.respond("-ERR [SYS/TEMP] account state unavailable, try again later")
				return
			}
			if !live {
				s.respond("-ERR account no longer available")
				return
			}
		}

		switch cmd {
		case "QUIT":
			s.handleQuit()
			return
		case "CAPA":
			s.handleCapa()
		case "USER":
			s.handleUser(args)
		case "PASS":
			s.handlePass(args)
		case "STLS":
			s.handleSTLS()
		case "STAT":
			s.handleStat()
		case "LIST":
			s.handleList(args)
		case "UIDL":
			s.handleUidl(args)
		case "RETR":
			s.handleRetr(args)
		case "TOP":
			s.handleTop(args)
		case "DELE":
			s.handleDele(args)
		case "NOOP":
			s.handleNoop()
		case "RSET":
			s.handleRset()
		default:
			s.respond("-ERR unknown command")
		}
	}
}

// extendDeadline refreshes the idle deadline (both read and write) for the next
// command/response cycle. A zero idleTimeout disables the deadline.
func (s *session) extendDeadline() {
	if s.idleTimeout > 0 {
		_ = s.conn.SetDeadline(time.Now().Add(s.idleTimeout))
	}
}

func (s *session) parseCommand(line string) (string, string) {
	parts := strings.SplitN(line, " ", 2)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}
	return cmd, args
}

func (s *session) respond(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	s.writeString(msg + "\r\n")
	s.flushWriter()
}

func (s *session) writeString(value string) {
	if _, err := s.writer.WriteString(value); err != nil {
		s.logger.Warn("pop3: failed to write response", "error", err)
	}
}

func (s *session) flushWriter() {
	if err := s.writer.Flush(); err != nil {
		s.logger.Warn("pop3: failed to flush response", "error", err)
	}
}

// accountLive reports whether the authenticated account is still enabled,
// still exists and still has the password it logged in with. It costs one
// Pebble Get per command. The check fails closed: a missing profile reports
// not live, and a store read error is returned so the caller refuses the
// command and closes without applying DELE marks. PASS also fails closed on
// any error.
func (s *session) accountLive() (bool, error) {
	st, err := s.auth.AccountState(s.userID)
	if err != nil {
		s.logger.Warn("pop3: account state check failed, refusing command", "user", s.userID, "error", err)
		return false, err
	}
	if st.Live(s.epoch) {
		return true, nil
	}
	s.logger.Info("pop3: closing session, account no longer valid", "user", s.userID,
		"missing", st.Missing, "disabled", st.Disabled)
	return false, nil
}

func (s *session) handleQuit() {
	if s.authenticated {
		s.applyDeletions()
	}
	s.respond("+OK bye")
}

func (s *session) handleCapa() {
	s.writeString("+OK capabilities\r\n")
	s.writeString("USER\r\n")
	s.writeString("UIDL\r\n")
	s.writeString("TOP\r\n")
	s.writeString("RESP-CODES\r\n")
	if !s.isTLS && s.tlsCfg != nil {
		s.writeString("STLS\r\n")
	}
	s.writeString("IMPLEMENTATION bongo-go\r\n")
	s.writeString(".\r\n")
	s.flushWriter()
}

func (s *session) handleUser(args string) {
	if s.authenticated {
		s.respond("-ERR already authenticated")
		return
	}
	if args == "" {
		s.respond("-ERR missing username")
		return
	}
	s.username = args
	s.respond("+OK")
}

func (s *session) handlePass(args string) {
	if s.authenticated {
		s.respond("-ERR already authenticated")
		return
	}
	if s.username == "" {
		s.respond("-ERR USER first")
		return
	}
	if args == "" {
		s.respond("-ERR missing password")
		return
	}

	// Require STLS before accepting credentials in the clear, unless running in
	// dev mode. This prevents sending the password over a plaintext connection.
	if !s.isTLS && !s.isDev && s.tlsCfg != nil {
		s.respond("-ERR must issue STLS before authenticating")
		return
	}

	// Brute-force defence: block the source IP after too many failed attempts.
	ip := s.clientIP()
	if s.authLimiter != nil && s.authLimiter.Blocked(ip) {
		s.logger.Warn("pop3: auth throttled after repeated failures", "username", s.username, "ip", ip)
		s.username = ""
		s.respond("-ERR too many failed attempts, try again later")
		return
	}

	userID, profile, err := s.auth.AuthenticateProfile(s.username, args)
	if err != nil {
		s.logger.Warn("pop3: auth failed", "username", s.username, "ip", ip)
		if s.authLimiter != nil {
			s.authLimiter.Fail(ip, s.username)
		}
		s.username = ""
		s.respond("-ERR authentication failed")
		return
	}

	if s.authLimiter != nil {
		s.authLimiter.Succeed(ip, s.username)
	}
	// Fail closed: the epoch must come from the profile the password was
	// verified against, not a later re-read a concurrent reset could change.
	if profile == nil || profile.Disabled {
		s.logger.Warn("pop3: auth refused, no usable profile", "username", s.username, "ip", ip)
		s.username = ""
		s.respond("-ERR authentication failed")
		return
	}
	s.epoch = auth.ProtocolEpoch(profile)
	s.userID = userID
	s.authenticated = true
	s.logger.Debug("pop3: auth succeeded", "username", s.username, "user_id", userID)

	if err := s.loadInboxSnapshot(); err != nil {
		s.logger.Error("pop3: failed to load inbox", "error", err)
		s.authenticated = false
		s.respond("-ERR failed to load mailbox")
		return
	}

	s.respond("+OK %d messages", len(s.messages))
}

func (s *session) handleSTLS() {
	if s.isTLS {
		s.respond("-ERR already using TLS")
		return
	}
	if s.tlsCfg == nil {
		s.respond("-ERR TLS not available")
		return
	}
	if s.authenticated {
		s.respond("-ERR STLS not allowed after authentication")
		return
	}

	s.respond("+OK begin TLS negotiation")

	tlsConn := tls.Server(s.conn, s.tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		s.logger.Warn("pop3: TLS handshake failed", "error", err)
		return
	}

	s.conn = tlsConn
	s.reader = bufio.NewReader(tlsConn)
	s.writer = bufio.NewWriter(tlsConn)
	s.isTLS = true
}

func (s *session) requireAuth() bool {
	if !s.authenticated {
		s.respond("-ERR not authenticated")
		return false
	}
	return true
}

func (s *session) handleStat() {
	if !s.requireAuth() {
		return
	}
	count, size := s.activeStats()
	s.respond("+OK %d %d", count, size)
}

func (s *session) handleList(args string) {
	if !s.requireAuth() {
		return
	}

	if args != "" {
		num, err := strconv.Atoi(args)
		if err != nil || num < 1 || num > len(s.messages) {
			s.respond("-ERR invalid message number")
			return
		}
		if s.deleted[num] {
			s.respond("-ERR message deleted")
			return
		}
		s.respond("+OK %d %d", num, s.messages[num-1].size)
		return
	}

	count, size := s.activeStats()
	fmt.Fprintf(s.writer, "+OK %d messages (%d octets)\r\n", count, size)
	for i, m := range s.messages {
		num := i + 1
		if s.deleted[num] {
			continue
		}
		fmt.Fprintf(s.writer, "%d %d\r\n", num, m.size)
	}
	s.writeString(".\r\n")
	s.flushWriter()
}

func (s *session) handleUidl(args string) {
	if !s.requireAuth() {
		return
	}

	if args != "" {
		num, err := strconv.Atoi(args)
		if err != nil || num < 1 || num > len(s.messages) {
			s.respond("-ERR invalid message number")
			return
		}
		if s.deleted[num] {
			s.respond("-ERR message deleted")
			return
		}
		s.respond("+OK %d %s", num, s.messages[num-1].guid)
		return
	}

	s.writeString("+OK\r\n")
	for i, m := range s.messages {
		num := i + 1
		if s.deleted[num] {
			continue
		}
		fmt.Fprintf(s.writer, "%d %s\r\n", num, m.guid)
	}
	s.writeString(".\r\n")
	s.flushWriter()
}

func (s *session) handleRetr(args string) {
	if !s.requireAuth() {
		return
	}

	num, err := strconv.Atoi(args)
	if err != nil || num < 1 || num > len(s.messages) {
		s.respond("-ERR invalid message number")
		return
	}
	if s.deleted[num] {
		s.respond("-ERR message deleted")
		return
	}

	msg := &s.messages[num-1]
	data, err := store.RawMessageBytes(s.blobs, msg.meta)
	if err != nil {
		s.logger.Error("pop3: failed to fetch blob", "hash", msg.blobHash, "error", err)
		s.respond("-ERR failed to retrieve message")
		return
	}

	s.respond("+OK %d octets", len(data))
	s.writeByteStuffed(data, -1)
}

func (s *session) handleTop(args string) {
	if !s.requireAuth() {
		return
	}

	parts := strings.SplitN(args, " ", 2)
	if len(parts) != 2 {
		s.respond("-ERR usage: TOP msg n")
		return
	}

	num, err := strconv.Atoi(parts[0])
	if err != nil || num < 1 || num > len(s.messages) {
		s.respond("-ERR invalid message number")
		return
	}
	if s.deleted[num] {
		s.respond("-ERR message deleted")
		return
	}

	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		s.respond("-ERR invalid line count")
		return
	}

	msg := &s.messages[num-1]
	data, err := store.RawMessageBytes(s.blobs, msg.meta)
	if err != nil {
		s.logger.Error("pop3: failed to fetch blob", "hash", msg.blobHash, "error", err)
		s.respond("-ERR failed to retrieve message")
		return
	}

	s.respond("+OK")
	s.writeByteStuffed(data, n)
}

func (s *session) handleDele(args string) {
	if !s.requireAuth() {
		return
	}

	num, err := strconv.Atoi(args)
	if err != nil || num < 1 || num > len(s.messages) {
		s.respond("-ERR invalid message number")
		return
	}
	if s.deleted[num] {
		s.respond("-ERR message already deleted")
		return
	}

	s.deleted[num] = true
	s.logger.Debug("pop3: message marked for deletion", "user", s.userID, "msg_num", num)
	s.respond("+OK message %d deleted", num)
}

func (s *session) handleNoop() {
	if !s.requireAuth() {
		return
	}
	s.respond("+OK")
}

func (s *session) handleRset() {
	if !s.requireAuth() {
		return
	}
	s.deleted = make(map[int]bool)
	s.respond("+OK")
}

// activeStats returns count and total size of non-deleted messages.
func (s *session) activeStats() (int, int64) {
	var count int
	var size int64
	for i, m := range s.messages {
		if !s.deleted[i+1] {
			count++
			size += m.size
		}
	}
	return count, size
}

// loadInboxSnapshot loads the INBOX messages into a frozen snapshot for this session.
func (s *session) loadInboxSnapshot() error {
	msgs, err := s.mailboxes.ListMessages(s.userID, "INBOX")
	if err != nil {
		return fmt.Errorf("listing inbox: %w", err)
	}

	s.messages = make([]popMsg, 0, len(msgs))
	for _, msg := range msgs {
		// Load the MailMeta to get the BlobRef.
		key := fmt.Sprintf("/user/%s/mail/%s", s.userID, msg.MailGuid)
		data, err := s.pebble.Get([]byte(key))
		if err != nil {
			s.logger.Warn("pop3: missing mail meta", "guid", msg.MailGuid, "error", err)
			continue
		}

		meta := &bongov1.MailMeta{}
		if err := proto.Unmarshal(data, meta); err != nil {
			s.logger.Warn("pop3: invalid mail meta", "guid", msg.MailGuid, "error", err)
			continue
		}

		if meta.RawMessage == nil {
			s.logger.Warn("pop3: no blob ref", "guid", msg.MailGuid)
			continue
		}

		s.messages = append(s.messages, popMsg{
			uid:      msg.Uid,
			guid:     msg.MailGuid,
			blobHash: meta.RawMessage.Hash,
			size:     meta.RawMessage.Size,
			meta:     meta,
		})
	}

	return nil
}

// writeByteStuffed writes message data with POP3 byte-stuffing.
// If bodyLines >= 0, only the headers and the first bodyLines lines of body are written (TOP command).
// If bodyLines < 0, the entire message is written (RETR command).
func (s *session) writeByteStuffed(data []byte, bodyLines int) {
	content := strings.TrimRight(string(data), "\r\n")
	lines := strings.Split(content, "\n")
	inBody := false
	bodyCount := 0

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")

		if !inBody && line == "" {
			inBody = true
			s.writeString("\r\n")
			continue
		}

		if inBody && bodyLines >= 0 {
			if bodyCount >= bodyLines {
				break
			}
			bodyCount++
		}

		// Byte-stuffing: lines starting with "." get an extra ".".
		if strings.HasPrefix(line, ".") {
			s.writeString(".")
		}
		s.writeString(line + "\r\n")
	}

	s.writeString(".\r\n")
	s.flushWriter()
}

// applyDeletions moves deleted messages to Trash.
func (s *session) applyDeletions() {
	moved := 0
	for num := range s.deleted {
		msg := &s.messages[num-1]
		if err := s.mailboxes.MoveMessage(s.userID, "INBOX", msg.uid, "Trash"); err != nil {
			s.logger.Error("pop3: failed to move message to trash",
				"uid", msg.uid, "guid", msg.guid, "user", s.userID, "error", err)
		} else {
			moved++
		}
	}
	if moved > 0 {
		s.logger.Info("pop3: messages moved to trash", "user", s.userID, "count", moved)
	}
}
