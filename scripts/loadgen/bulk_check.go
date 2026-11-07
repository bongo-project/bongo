package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cockroachdb/pebble"
	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The equivalence check reduces a store to a canonical per-user description
// and compares two of them. Users are keyed by email and messages by
// Message-ID, because GUIDs and user IDs are random. A key that matched
// nothing would report nothing, so every dump carries counts and the check
// refuses to compare two empty stores.

// ignoredFields are listed in the output so a reader sees what the check
// does not look at.
var ignoredFields = []string{
	"user IDs and message GUIDs",
	"received and internal dates",
	"UIDVALIDITY",
	"password hash and other credentials",
	"the stored skeleton or whole-message blob hash (it carries the stamped headers)",
	"SPF, DKIM and DMARC results and the spam score",
	"trace headers at the head of a message (Received, Received-SPF, Authentication-Results, X-Spam-*): their bytes are subtracted from the size and shown as the trace delta",
}

type msgDump struct {
	Folder      string
	UID         uint32
	Flags       uint32
	Tags        []string
	Split       bool
	Attachments []string // sorted "hash:size"
	Size        int      // message bytes without the leading trace headers
	TraceDelta  int      // bytes of those headers
}

type mailboxDump struct {
	UIDNext      uint32
	Unsubscribed bool
	Messages     int
}

type userDump struct {
	Admin, Disabled bool
	Kind            string
	Addresses       []string
	Mailboxes       map[string]mailboxDump
	Messages        map[string]msgDump
	IndexRows       int // SQLite mail rows; -1 when the index was not read
}

type storeDump struct {
	Users    map[string]*userDump
	Messages int
	Problems []string // blobs missing, unreadable records: never a "match"
	Excluded int      // users left out because their address is outside the test domain
}

// storeView is what a dump reads. meta may be nil, which skips the SQLite
// row count; that leaves IndexRows at -1, which compareUsers refuses to treat
// as a match. A non-empty domain keeps only users whose address is in it, so
// an operator account the plan never made does not fail the check.
type storeView struct {
	ps     *store.PebbleStore
	blobs  *store.BlobStore
	meta   *store.MetadataManager
	domain string
}

// emailInDomain reports whether email ends in "@"+domain. A bare suffix test
// would let "xbulk.test" through for "bulk.test".
func emailInDomain(email, domain string) bool {
	return strings.HasSuffix(strings.ToLower(email), "@"+strings.ToLower(domain))
}

func dumpStore(v storeView) (*storeDump, error) {
	d := &storeDump{Users: map[string]*userDump{}}
	ms := store.NewMailboxStore(v.ps, nil)

	owned := map[string][]string{} // user ID -> addresses
	it, err := v.ps.PrefixScan([]byte("/system/address/"))
	if err != nil {
		return nil, fmt.Errorf("scan addresses: %w", err)
	}
	for it.Next() {
		val, err := it.Value()
		if err != nil {
			_ = it.Close()
			return nil, fmt.Errorf("read address: %w", err)
		}
		var rec bongov1.AddressRecord
		if proto.Unmarshal(val, &rec) == nil {
			addr := strings.TrimPrefix(string(it.Key()), "/system/address/")
			owned[rec.UserId] = append(owned[rec.UserId], addr+" ("+rec.Kind+")")
		}
	}
	if err := it.Close(); err != nil {
		return nil, fmt.Errorf("scan addresses: %w", err)
	}

	pit, err := v.ps.PrefixScan([]byte("/user/"))
	if err != nil {
		return nil, fmt.Errorf("scan users: %w", err)
	}
	type prof struct {
		id string
		p  *bongov1.UserProfile
	}
	var profiles []*prof
	for pit.Next() {
		k := string(pit.Key())
		parts := strings.Split(k, "/") // "", user, id, profile
		if len(parts) != 4 || parts[3] != "profile" {
			continue
		}
		val, err := pit.Value()
		if err != nil {
			_ = pit.Close()
			return nil, fmt.Errorf("read profile: %w", err)
		}
		pr := &prof{id: parts[2], p: &bongov1.UserProfile{}}
		if err := proto.Unmarshal(val, pr.p); err != nil {
			d.Problems = append(d.Problems, "unreadable profile "+parts[2])
			continue
		}
		profiles = append(profiles, pr)
	}
	if err := pit.Close(); err != nil {
		return nil, fmt.Errorf("scan users: %w", err)
	}

	for _, pr := range profiles {
		email := pr.p.Email
		if v.domain != "" && !emailInDomain(email, v.domain) {
			d.Excluded++
			continue
		}
		u := &userDump{
			Admin: pr.p.IsAdmin, Disabled: pr.p.Disabled, Kind: pr.p.Kind,
			Addresses: owned[pr.id], Mailboxes: map[string]mailboxDump{},
			Messages: map[string]msgDump{}, IndexRows: -1,
		}
		slices.Sort(u.Addresses)
		d.Users[email] = u
		boxes, err := ms.ListMailboxes(pr.id)
		if err != nil {
			return nil, fmt.Errorf("list mailboxes of %s: %w", email, err)
		}
		for _, mb := range boxes {
			msgs, err := ms.ListMessages(pr.id, mb.Name)
			if err != nil {
				return nil, fmt.Errorf("list messages of %s/%s: %w", email, mb.Name, err)
			}
			if p := denseUIDs(msgs, mb.UidNext); p != "" {
				d.Problems = append(d.Problems, fmt.Sprintf("%s: mailbox %s: %s", email, mb.Name, p))
			}
			u.Mailboxes[mb.Name] = mailboxDump{UIDNext: mb.UidNext, Unsubscribed: mb.Unsubscribed, Messages: len(msgs)}
			for _, m := range msgs {
				key, md, problem := describeMessage(v, pr.id, mb.Name, m)
				if problem != "" {
					d.Problems = append(d.Problems, email+": "+problem)
				}
				if _, dup := u.Messages[key]; dup {
					key = fmt.Sprintf("%s#%s/%d", key, mb.Name, m.Uid)
				}
				u.Messages[key] = md
				d.Messages++
			}
		}
		if v.meta != nil {
			objs, err := v.meta.QueryByType(pr.id, store.DocTypeMail)
			if err != nil {
				return nil, fmt.Errorf("count index rows of %s: %w", email, err)
			}
			u.IndexRows = len(objs)
		}
	}
	return d, nil
}

// denseUIDs reports a mailbox whose UIDs are not exactly 1..n with uid_next
// n+1, which is what allocation without expunges gives.
func denseUIDs(msgs []*bongov1.MailboxMessage, uidNext uint32) string {
	seen := make(map[uint32]bool, len(msgs))
	for _, m := range msgs {
		if m.Uid < 1 || int(m.Uid) > len(msgs) || seen[m.Uid] {
			return fmt.Sprintf("UIDs are not 1..%d (uid %d)", len(msgs), m.Uid)
		}
		seen[m.Uid] = true
	}
	if int(uidNext) != len(msgs)+1 {
		return fmt.Sprintf("uid_next is %d for %d messages", uidNext, len(msgs))
	}
	return ""
}

func describeMessage(v storeView, userID, folder string, m *bongov1.MailboxMessage) (key string, md msgDump, problem string) {
	md = msgDump{Folder: folder, UID: m.Uid, Flags: m.Flags, Tags: slices.Sorted(slices.Values(m.Tags))}
	key = fmt.Sprintf("(no message-id) %s/%d", folder, m.Uid)
	val, err := v.ps.Get([]byte("/user/" + userID + "/mail/" + m.MailGuid))
	if err != nil {
		return key, md, fmt.Sprintf("%s/%d: mail record unreadable: %v", folder, m.Uid, err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(val, &meta); err != nil {
		return key, md, fmt.Sprintf("%s/%d: mail record corrupt", folder, m.Uid)
	}
	if meta.MessageId != "" {
		key = meta.MessageId
	}
	md.Split = len(meta.Layout) > 0
	var missing []string
	for _, a := range meta.Attachments {
		md.Attachments = append(md.Attachments, fmt.Sprintf("%s:%d", a.Hash, a.Size))
		if a.Hash != "" && !v.blobs.Exists(a.Hash) {
			missing = append(missing, a.Hash)
		}
	}
	if len(missing) > 0 {
		problem = fmt.Sprintf("%s: attachment blob %s missing", key, strings.Join(missing, ", "))
	}
	slices.Sort(md.Attachments)
	raw, err := store.FullRawMessage(&meta, func(h string) ([]byte, error) {
		rc, err := v.blobs.Get(h)
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	})
	if err != nil {
		unreadable := fmt.Sprintf("%s: message unreadable: %v", key, err)
		if problem != "" {
			unreadable = problem + "; " + unreadable
		}
		return key, md, unreadable
	}
	body := stripTraceHeaders(raw)
	md.Size = len(body)
	md.TraceDelta = len(raw) - len(body)
	return key, md, problem
}

// stripTraceHeaders removes the run of trace headers a server puts at the
// head of a message. It stops at the first header that is not one, so the
// sender's own headers are never touched.
func stripTraceHeaders(raw []byte) []byte {
	rest := raw
	for len(rest) > 0 {
		nl := bytes.Index(rest, []byte("\r\n"))
		if nl <= 0 {
			break
		}
		name, _, ok := bytes.Cut(rest[:nl], []byte(":"))
		if !ok || !isTraceHeader(strings.ToLower(string(name))) {
			break
		}
		end := nl + 2
		for end < len(rest) && (rest[end] == ' ' || rest[end] == '\t') {
			j := bytes.Index(rest[end:], []byte("\r\n"))
			if j < 0 {
				end = len(rest)
				break
			}
			end += j + 2
		}
		rest = rest[end:]
	}
	return rest
}

func isTraceHeader(lower string) bool {
	switch lower {
	case "received", "received-spf", "authentication-results":
		return true
	}
	return strings.HasPrefix(lower, "x-spam-")
}

// compareDumps lists every difference between two dumps, sorted. Problems
// found while reading either side are reported too.
func compareDumps(a, b *storeDump) []string {
	var diffs []string
	for _, p := range a.Problems {
		diffs = append(diffs, "A unreadable: "+p)
	}
	for _, p := range b.Problems {
		diffs = append(diffs, "B unreadable: "+p)
	}
	for _, email := range sortedKeys(a.Users) {
		ua, ub := a.Users[email], b.Users[email]
		if ub == nil {
			diffs = append(diffs, "user "+email+": only in A")
			continue
		}
		diffs = append(diffs, compareUsers(email, ua, ub)...)
	}
	for _, email := range sortedKeys(b.Users) {
		if a.Users[email] == nil {
			diffs = append(diffs, "user "+email+": only in B")
		}
	}
	return diffs
}

func compareUsers(email string, a, b *userDump) []string {
	var diffs []string
	add := func(what string, x, y any) {
		diffs = append(diffs, fmt.Sprintf("user %s: %s: A=%v B=%v", email, what, x, y))
	}
	if a.Admin != b.Admin || a.Disabled != b.Disabled || a.Kind != b.Kind {
		add("profile (admin, disabled, kind)", fmt.Sprint(a.Admin, a.Disabled, a.Kind), fmt.Sprint(b.Admin, b.Disabled, b.Kind))
	}
	if !slices.Equal(a.Addresses, b.Addresses) {
		add("addresses", a.Addresses, b.Addresses)
	}
	// -1 means the index was not read; two unread indexes are no evidence.
	if a.IndexRows != b.IndexRows || a.IndexRows < 0 {
		add("SQLite mail rows", a.IndexRows, b.IndexRows)
	}
	for _, name := range sortedKeys(a.Mailboxes) {
		ma, mb := a.Mailboxes[name], b.Mailboxes[name]
		if _, ok := b.Mailboxes[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("user %s: mailbox %s: only in A", email, name))
			continue
		}
		if ma != mb {
			add("mailbox "+name, fmt.Sprintf("%+v", ma), fmt.Sprintf("%+v", mb))
		}
	}
	for _, name := range sortedKeys(b.Mailboxes) {
		if _, ok := a.Mailboxes[name]; !ok {
			diffs = append(diffs, fmt.Sprintf("user %s: mailbox %s: only in B", email, name))
		}
	}
	for _, id := range sortedKeys(a.Messages) {
		ma, ok := b.Messages[id]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("user %s: message %s: only in A (folder %s)", email, id, a.Messages[id].Folder))
			continue
		}
		diffs = append(diffs, compareMessages(email, id, a.Messages[id], ma)...)
	}
	for _, id := range sortedKeys(b.Messages) {
		if _, ok := a.Messages[id]; !ok {
			diffs = append(diffs, fmt.Sprintf("user %s: message %s: only in B (folder %s)", email, id, b.Messages[id].Folder))
		}
	}
	return diffs
}

func compareMessages(email, id string, a, b msgDump) []string {
	var diffs []string
	field := func(name string, x, y any) {
		if fmt.Sprint(x) != fmt.Sprint(y) {
			diffs = append(diffs, fmt.Sprintf("user %s: message %s: %s: A=%v B=%v", email, id, name, x, y))
		}
	}
	field("folder", a.Folder, b.Folder)
	// The UID is not compared: SMTP mail is delivered by concurrent pipeline
	// workers, so which message gets which UID is the delivery order, not the
	// plan order. dumpStore checks instead that each mailbox's UIDs are
	// dense, 1 to n.
	field("flags", a.Flags, b.Flags)
	field("tags", a.Tags, b.Tags)
	field("stored split", a.Split, b.Split)
	field("attachment blobs (hash:size)", a.Attachments, b.Attachments)
	field("size without trace headers", a.Size, b.Size)
	// The trace delta is informational: a loader has no Received header, and
	// a different delta with an equal size is expected. It is reported only
	// as a total by the caller.
	return diffs
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	})
}

// traceDeltaTotal sums the trace-header bytes over a dump, for the report.
func traceDeltaTotal(d *storeDump) (total int64) {
	for _, u := range d.Users {
		for _, m := range u.Messages {
			total += int64(m.TraceDelta)
		}
	}
	return total
}

// openCheckStore opens a stopped server's data directory: Pebble read-only,
// and the SQLite index through the server's own manager. That manager is not
// read-only and may create files under metadata/, so the directory must not
// be in use.
func openCheckStore(dir string) (storeView, func(), error) {
	if !filepath.IsAbs(dir) {
		return storeView{}, nil, fmt.Errorf("%q must be an absolute path", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "pebble")); err != nil {
		return storeView{}, nil, fmt.Errorf("%s holds no pebble directory: %w", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, bulkMarker)); err == nil {
		return storeView{}, nil, fmt.Errorf("%s holds %s: the load did not finish", dir, bulkMarker)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return storeView{}, nil, err
	}
	ps, err := store.NewPebbleStore(filepath.Join(dir, "pebble"), func(o *pebble.Options) { o.ReadOnly = true })
	if err != nil {
		return storeView{}, nil, fmt.Errorf("%w (is the server running?): %w", errDataDirInUse, err)
	}
	blobs, err := store.NewBlobStore(dir)
	if err != nil {
		_ = ps.Close()
		return storeView{}, nil, fmt.Errorf("open blob store: %w", err)
	}
	meta, err := store.NewMetadataManager(dir)
	if err != nil {
		_ = ps.Close()
		return storeView{}, nil, fmt.Errorf("open metadata manager: %w", err)
	}
	return storeView{ps: ps, blobs: blobs, meta: meta}, func() { _ = meta.Close(); _ = ps.Close() }, nil
}

func runBulkCheck(cfg Config, stdout, stderr io.Writer) int {
	o := cfg.Bulk
	if o.CheckA == "" || o.CheckB == "" {
		fmt.Fprintln(stderr, "loadgen: bulk: check mode needs both --bulk-check-a and --bulk-check-b")
		return exitRefuse
	}
	if o.CheckA == o.CheckB {
		fmt.Fprintln(stderr, "loadgen: bulk: --bulk-check-a and --bulk-check-b name the same directory")
		return exitRefuse
	}
	domain, err := checkTestDomain(cfg.TestDomain)
	if err != nil {
		fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
		return exitRefuse
	}
	var dumps [2]*storeDump
	for i, dir := range []string{o.CheckA, o.CheckB} {
		v, closeFn, err := openCheckStore(dir)
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
			return exitRefuse
		}
		v.domain = domain
		d, err := dumpStore(v)
		closeFn()
		if err != nil {
			fmt.Fprintf(stderr, "loadgen: bulk: %v\n", err)
			return exitFail
		}
		dumps[i] = d
	}
	return reportCheck(dumps[0], dumps[1], o.MaxDiffs, stdout, stderr)
}

// reportCheck prints the comparison and returns the exit code: 1 on any
// difference, and also when there is nothing to compare, since two empty
// stores would "match" without proving anything.
func reportCheck(a, b *storeDump, maxDiffs int, stdout, stderr io.Writer) int {
	fmt.Fprintf(stdout, "A: %d users, %d messages\n", len(a.Users), a.Messages)
	fmt.Fprintf(stdout, "B: %d users, %d messages\n", len(b.Users), b.Messages)
	fmt.Fprintf(stdout, "users excluded as outside the test domain: A=%d B=%d\n", a.Excluded, b.Excluded)
	fmt.Fprintf(stdout, "trace header bytes ignored: A=%d B=%d\n", traceDeltaTotal(a), traceDeltaTotal(b))
	fmt.Fprintln(stdout, "ignored fields:")
	for _, f := range ignoredFields {
		fmt.Fprintf(stdout, "  - %s\n", f)
	}
	if len(a.Users) == 0 || len(b.Users) == 0 || a.Messages == 0 || b.Messages == 0 {
		fmt.Fprintln(stderr, "loadgen: bulk: a side has no users or no messages, so there is nothing to compare")
		return exitFail
	}
	diffs := compareDumps(a, b)
	if len(diffs) == 0 {
		fmt.Fprintln(stdout, "stores match")
		return exitOK
	}
	fmt.Fprintf(stdout, "%d differences\n", len(diffs))
	for i, d := range diffs {
		if maxDiffs > 0 && i >= maxDiffs {
			fmt.Fprintf(stdout, "... %d more\n", len(diffs)-i)
			break
		}
		fmt.Fprintln(stdout, "  "+d)
	}
	return exitFail
}
