package pop3

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/auth"
	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// popMsg represents a message in the POP3 session snapshot.
type popMsg struct {
	uid      uint32
	guid     string
	blobHash string
	size     int64
}

// session handles a single POP3 connection.
type session struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer

	auth      *auth.LocalAuth
	mailboxes *store.MailboxStore
	pebble    *store.PebbleStore
	blobs     *store.BlobStore
	tlsCfg    *tls.Config
	isTLS     bool
	isDev     bool
	logger    *slog.Logger

	// Authorization state.
	username string

	// Transaction state.
	authenticated bool
	userID        string
	messages      []popMsg // 0-indexed; POP3 message numbers are 1-based
	deleted       map[int]bool
}

func newSession(
	conn net.Conn,
	authenticator *auth.LocalAuth,
	mailboxes *store.MailboxStore,
	pebble *store.PebbleStore,
	blobs *store.BlobStore,
	tlsCfg *tls.Config,
	isTLS bool,
	isDev bool,
	logger *slog.Logger,
) *session {
	return &session{
		conn:    conn,
		reader:  bufio.NewReader(conn),
		writer:  bufio.NewWriter(conn),
		auth:    authenticator,
		mailboxes: mailboxes,
		pebble:  pebble,
		blobs:   blobs,
		tlsCfg:  tlsCfg,
		isTLS:   isTLS,
		isDev:   isDev,
		logger:  logger,
		deleted: make(map[int]bool),
	}
}

func (s *session) handle() {
	defer s.conn.Close()

	s.respond("+OK bongo-go POP3 ready")

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		cmd, args := s.parseCommand(line)
		cmd = strings.ToUpper(cmd)

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
	s.writer.WriteString(msg + "\r\n")
	s.writer.Flush()
}

func (s *session) handleQuit() {
	if s.authenticated {
		s.applyDeletions()
	}
	s.respond("+OK bye")
}

func (s *session) handleCapa() {
	s.writer.WriteString("+OK capabilities\r\n")
	s.writer.WriteString("USER\r\n")
	s.writer.WriteString("UIDL\r\n")
	s.writer.WriteString("TOP\r\n")
	if !s.isTLS && s.tlsCfg != nil {
		s.writer.WriteString("STLS\r\n")
	}
	s.writer.WriteString("IMPLEMENTATION bongo-go\r\n")
	s.writer.WriteString(".\r\n")
	s.writer.Flush()
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

	userID, err := s.auth.Authenticate(s.username, args)
	if err != nil {
		s.logger.Warn("pop3: auth failed", "username", s.username)
		s.username = ""
		s.respond("-ERR authentication failed")
		return
	}

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
	s.writer.WriteString(fmt.Sprintf("+OK %d messages (%d octets)\r\n", count, size))
	for i, m := range s.messages {
		num := i + 1
		if s.deleted[num] {
			continue
		}
		s.writer.WriteString(fmt.Sprintf("%d %d\r\n", num, m.size))
	}
	s.writer.WriteString(".\r\n")
	s.writer.Flush()
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

	s.writer.WriteString("+OK\r\n")
	for i, m := range s.messages {
		num := i + 1
		if s.deleted[num] {
			continue
		}
		s.writer.WriteString(fmt.Sprintf("%d %s\r\n", num, m.guid))
	}
	s.writer.WriteString(".\r\n")
	s.writer.Flush()
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
	data, err := s.fetchBlob(msg.blobHash)
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
	data, err := s.fetchBlob(msg.blobHash)
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

		var meta bongov1.MailMeta
		if err := proto.Unmarshal(data, &meta); err != nil {
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
		})
	}

	return nil
}

// fetchBlob reads the entire blob content.
func (s *session) fetchBlob(hash string) ([]byte, error) {
	rc, err := s.blobs.Get(hash)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
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
			s.writer.WriteString("\r\n")
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
			s.writer.WriteString(".")
		}
		s.writer.WriteString(line + "\r\n")
	}

	s.writer.WriteString(".\r\n")
	s.writer.Flush()
}

// applyDeletions moves deleted messages to Trash.
func (s *session) applyDeletions() {
	for num := range s.deleted {
		msg := &s.messages[num-1]
		if err := s.mailboxes.MoveMessage(s.userID, "INBOX", msg.uid, "Trash"); err != nil {
			s.logger.Error("pop3: failed to move message to trash",
				"uid", msg.uid, "guid", msg.guid, "error", err)
		}
	}
}
