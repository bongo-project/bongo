# Sprint 12a: POP3 Server

**Phase:** 1 (Foundation)
**Goal:** Add POP3 (RFC 1939) mail access for legacy clients, following the established
SMTP/IMAP server pattern.

---

## Context

Sprints 01–11 deliver SMTP inbound, IMAP access, and a full web UI. POP3 is the last
mail access protocol needed for legacy client support. It's a simple download-and-delete
protocol — much simpler than IMAP.

**Existing infrastructure:**
- SMTP server pattern: `internal/smtp/server.go` — Server struct, Listen(), Serve(), Close()
- IMAP server pattern: `internal/imap/server.go` — identical lifecycle
- POP3 config already defined: `POP3Config{Enabled:true, Port:110, PortTLS:995}`
- Auth: `auth.LocalAuth.Authenticate(email, password)` returns userID
- Mailbox store: `MailboxStore.ListMessages(userID, "INBOX")` returns messages
- Blob store: `BlobStore.Get(hash)` returns raw message bytes
- Agent lifecycle: SMTP startup at lines 509–531, IMAP at 576–614, shutdown at 789–805

---

## Tasks

### 1. POP3 protocol implementation

Implement the POP3 protocol handler as a standalone package.

**Create `internal/pop3/server.go`:**
- `Server` struct: auth, mailboxStore, blobStore, tlsCfg, logger, listeners, wg
- `NewServer(auth, mailboxes, blobs, tlsCfg, isDev, logger) *Server`
- `Listen(plainAddr, tlsAddr string) error` — open two net.Listeners (plain + TLS)
- `Serve() error` — accept loop, spawn `handleConn()` goroutines with WaitGroup
- `Close() error` — close listeners, wait for active connections via WaitGroup

**Create `internal/pop3/session.go`:**
- `session` struct: conn, reader (bufio.Reader), writer, authenticated bool,
  userID, messages []popMsg (snapshot), deleted map[int]bool
- `popMsg` struct: uid uint32, guid string, blobHash string, size int64
- `handle()` — send greeting `+OK bongo-go POP3 ready`, enter command loop
- Command dispatch (case-insensitive):
  - **Authorization state:**
    - `USER <name>` → store username, respond `+OK`
    - `PASS <password>` → call `auth.Authenticate(user, pass)`:
      - Success: load INBOX snapshot, transition to Transaction state, `+OK`
      - Failure: `−ERR authentication failed`
    - `CAPA` → list capabilities: USER, UIDL, TOP, STLS, IMPLEMENTATION
    - `STLS` → upgrade connection to TLS via `tls.Server(conn, tlsCfg)`, reset reader/writer
    - `QUIT` → `+OK` and close
  - **Transaction state:**
    - `STAT` → `+OK {count} {totalBytes}` (excluding deleted)
    - `LIST [msg]` → one line per message `{num} {size}`, or specific message
    - `UIDL [msg]` → `{num} {guid}` for unique-id listing
    - `RETR <msg>` → fetch blob, write with byte-stuffing (`.` → `..`), end with `.\r\n`
    - `TOP <msg> <n>` → headers + blank line + first n lines of body, byte-stuffed
    - `DELE <msg>` → mark for deletion, `+OK`
    - `NOOP` → `+OK`
    - `RSET` → unmark all deletions, `+OK`
    - `QUIT` → apply deletions (move to Trash), `+OK`, close
- INBOX snapshot loaded on successful auth: scan INBOX messages, build popMsg list
  with sequential numbering (1-based). Snapshot is frozen for the session (POP3 model).
- Byte-stuffing: scan each line of RETR/TOP output; if starts with `.`, prepend another `.`
- Error handling: unknown command → `−ERR`, wrong state → `−ERR`

**Done when:** A POP3 client can connect, authenticate, list messages, retrieve message
content, delete messages (moved to Trash on QUIT), and upgrade to TLS via STLS.

### 2. Wire POP3 into agent lifecycle

Integrate the POP3 server into agent startup and shutdown.

**Modify `internal/agent/agent.go`:**
- Add `POP3 *pop3.Server` field to Agent struct
- In `Run()`, after IMAP startup block (~line 614):
  ```
  if a.Config.POP3.Enabled {
      isDev := a.Config.Server.Role == "dev"
      pop3Port, pop3TLS := a.Config.POP3.Port, a.Config.POP3.PortTLS
      if isDev { pop3Port, pop3TLS = 1110, 1995 }
      a.POP3 = pop3.NewServer(a.Auth, a.Mailboxes, a.Blobs, tlsCfg, isDev, a.logger)
      plainAddr := net.JoinHostPort(bindAddr, strconv.Itoa(pop3Port))
      tlsAddr := net.JoinHostPort(bindAddr, strconv.Itoa(pop3TLS))
      a.POP3.Listen(plainAddr, tlsAddr)
      go a.POP3.Serve()
      logger.Info("pop3: started", "plain", plainAddr, "tls", tlsAddr)
  }
  ```
- In `Shutdown()`, after IMAP close (~line 805):
  ```
  if a.POP3 != nil { a.POP3.Close() }
  ```

**Done when:** `bongo-go serve --dev` starts POP3 on ports 1110/1995. Clean shutdown
closes POP3 listeners. Config `pop3.enabled = false` skips POP3 entirely.

### 3. Integration tests

**Create `test/integration/pop3_test.go` (build tag `integration`):**

**TestPOP3Authentication:**
1. Start dev agent with POP3 enabled
2. Connect via `net.Dial("tcp", "127.0.0.1:1110")`
3. Read greeting `+OK`
4. Send `USER admin@localhost\r\n` → verify `+OK`
5. Send `PASS admin\r\n` → verify `+OK`
6. Send `QUIT\r\n` → verify `+OK`, connection closed
7. Reconnect, send bad password → verify `−ERR`

**TestPOP3MailRetrieval:**
1. Start dev agent, deliver test email via SMTP (net/smtp to port 2525)
2. Wait for Raft propagation
3. Connect POP3, authenticate
4. `STAT` → verify `+OK 1 {size}`
5. `LIST` → verify one message line
6. `UIDL` → verify unique ID present
7. `RETR 1` → verify message content contains test subject/body
8. `TOP 1 5` → verify headers present + limited body lines

**TestPOP3DeleteAndQuit:**
1. Deliver email, connect POP3, authenticate
2. `STAT` → 1 message
3. `DELE 1` → `+OK`
4. `STAT` → still shows 1 (not committed until QUIT)
5. `QUIT` → message moved to Trash
6. Reconnect, authenticate → `STAT` → 0 messages

**TestPOP3STLS:**
1. Connect plaintext on port 1110
2. `CAPA` → verify STLS listed
3. `STLS` → `+OK`
4. Upgrade to TLS (`tls.Client(conn, &tls.Config{InsecureSkipVerify: true})`)
5. `CAPA` → verify connection works over TLS (STLS no longer listed)
6. Authenticate and STAT succeeds

**Done when:** `go test -race -tags=integration ./test/integration/pop3_test.go` passes.

---

## Exit Criteria

- [ ] POP3 server handles USER, PASS, STAT, LIST, RETR, DELE, QUIT, UIDL, TOP, CAPA, STLS, NOOP, RSET
- [ ] Byte-stuffing correct on RETR and TOP output
- [ ] INBOX snapshot frozen for session duration (POP3 lock model)
- [ ] DELE marks for deletion; QUIT applies deletions (move to Trash)
- [ ] RSET unmarks all deletions
- [ ] STLS upgrades plaintext connection to TLS
- [ ] POP3 integrated into agent lifecycle (start + graceful shutdown)
- [ ] Dev mode ports: 1110 plain, 1995 TLS
- [ ] Config `pop3.enabled = false` disables POP3
- [ ] `go test -race ./...` passes
- [ ] POP3 integration tests pass (auth, retrieval, delete, STLS)

## What NOT to do in this sprint

- No POP3 APOP authentication (legacy MD5-based, not worth implementing)
- No POP3 AUTH command / SASL (PLAIN USER/PASS is sufficient with STLS)
- No concurrent session locking (RFC says one session per mailbox; log warning if second connects)
- No spam scoring or mail filters (Sprint 12b and 12c)
