package main

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
)

// The defaults are stated assumptions, not measurements: production has too
// little traffic to measure a mix from. They are printed by seed -h.
const (
	defaultSizeMix = "2KB:35,8KB:30,32KB:20,128KB:10,1MB:5"
	// INBOX only, so a default run delivers about N x M messages. A mix
	// naming other folders plans them but counts them as deferred until
	// IMAP APPEND exists.
	defaultFolderMix = "INBOX:1"
	// defaultDedupMin matches storage.attachment_dedup_min_size.
	defaultDedupMin   = 32 * 1024
	defaultDedupShare = 0.5
	defaultPoolSize   = 8

	// messageOverhead is the room kept for headers, MIME framing and a
	// minimal text part around a pooled attachment.
	messageOverhead = 2048
	minMessageSize  = 512
	inboxFolder     = "INBOX"
	senderLocalPart = "loadgen-sender"
)

type weighted[T any] struct {
	Val    T
	Weight int
}

// parseMix parses "key:weight,key:weight". Weights must be positive integers.
func parseMix[T any](s string, parseKey func(string) (T, error)) ([]weighted[T], error) {
	var out []weighted[T]
	for _, item := range strings.Split(s, ",") {
		k, w, ok := strings.Cut(strings.TrimSpace(item), ":")
		if !ok {
			return nil, fmt.Errorf("mix item %q is not key:weight", item)
		}
		weight, err := strconv.Atoi(strings.TrimSpace(w))
		if err != nil || weight <= 0 {
			return nil, fmt.Errorf("mix item %q: weight must be a positive integer", item)
		}
		v, err := parseKey(strings.TrimSpace(k))
		if err != nil {
			return nil, fmt.Errorf("mix item %q: %w", item, err)
		}
		out = append(out, weighted[T]{v, weight})
	}
	if len(out) == 0 {
		return nil, errors.New("empty mix")
	}
	return out, nil
}

func canonMix[T any](m []weighted[T]) string {
	parts := make([]string, len(m))
	for i, w := range m {
		parts[i] = fmt.Sprintf("%v:%d", w.Val, w.Weight)
	}
	return strings.Join(parts, ",")
}

func pick[T any](rng *rand.Rand, m []weighted[T]) T {
	total := 0
	for _, w := range m {
		total += w.Weight
	}
	n := rng.IntN(total)
	for _, w := range m {
		if n < w.Weight {
			return w.Val
		}
		n -= w.Weight
	}
	return m[len(m)-1].Val
}

func parseSize(s string) (int, error) {
	u := strings.ToUpper(s)
	mult := 1
	switch {
	case strings.HasSuffix(u, "MB"):
		mult, u = 1<<20, strings.TrimSuffix(u, "MB")
	case strings.HasSuffix(u, "KB"):
		mult, u = 1<<10, strings.TrimSuffix(u, "KB")
	case strings.HasSuffix(u, "B"):
		u = strings.TrimSuffix(u, "B")
	}
	n, err := strconv.Atoi(u)
	if err != nil || n <= 0 || n > 64<<20/mult {
		return 0, fmt.Errorf("bad size %q (use B, KB or MB, at most 64MB)", s)
	}
	return n * mult, nil
}

var folderName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func parseFolder(s string) (string, error) {
	if !folderName.MatchString(s) {
		return "", fmt.Errorf("bad folder name %q", s)
	}
	return s, nil
}

// PlanSpec is everything that decides the plan. Two equal specs give equal
// plans; the state file header binds every field.
type PlanSpec struct {
	Seed       uint64
	Users      int
	MPU        int
	Domain     string
	Sizes      []weighted[int]
	Folders    []weighted[string]
	DedupShare float64
	DedupMin   int
	PoolSize   int
}

func newPlanSpec(domain string, o SeedOptions) (PlanSpec, error) {
	if o.Users < 1 || o.Users > 10_000_000 {
		return PlanSpec{}, errors.New("--users must be between 1 and 10000000")
	}
	if o.MessagesPerUser < 0 || o.MessagesPerUser > 100_000 {
		return PlanSpec{}, errors.New("--messages-per-user must be between 0 and 100000")
	}
	if math.IsNaN(o.DedupShare) || o.DedupShare < 0 || o.DedupShare > 1 {
		return PlanSpec{}, errors.New("--dedup-share must be between 0 and 1")
	}
	if o.DedupMin < 1 || o.PoolSize < 1 || o.PoolSize > 256 {
		return PlanSpec{}, errors.New("--dedup-min-size must be positive and --attachment-pool between 1 and 256")
	}
	sizes, err := parseMix(o.SizeMix, parseSize)
	if err != nil {
		return PlanSpec{}, fmt.Errorf("--size-mix: %w", err)
	}
	folders, err := parseMix(o.FolderMix, parseFolder)
	if err != nil {
		return PlanSpec{}, fmt.Errorf("--folder-mix: %w", err)
	}
	return PlanSpec{
		Seed: o.Seed, Users: o.Users, MPU: o.MessagesPerUser, Domain: domain,
		Sizes: sizes, Folders: folders, DedupShare: o.DedupShare,
		DedupMin: o.DedupMin, PoolSize: o.PoolSize,
	}, nil
}

// MessagePlan is one message. Attach is a pool index, or -1 for none.
type MessagePlan struct {
	User     int
	Index    int
	ID       string
	Size     int
	Folder   string
	Attach   int
	BodySeed uint64
}

type UserPlan struct {
	Index    int
	Email    string
	Messages []MessagePlan
}

func (s PlanSpec) email(i int) string { return fmt.Sprintf("u%06d@%s", i, s.Domain) }

func (s PlanSpec) sender() string { return senderLocalPart + "@" + s.Domain }

func (s PlanSpec) messageID(u, m int) string {
	return fmt.Sprintf("<loadgen-%x-u%d-m%d@%s>", s.Seed, u, m, s.Domain)
}

// poolRawLen is the raw byte length of pool entry j. Entries grow with j so
// a message of a given size can use the prefix that fits. Every entry's
// encoded body is above DedupMin, which is what makes StoreSplit take it.
func (s PlanSpec) poolRawLen(j int) int { return s.DedupMin*3/4 + 512 + 768*j }

// b64Len is the length of n bytes base64-encoded and wrapped at 76 columns
// with CRLF between lines, which is how encodeAttachment writes them.
func b64Len(n int) int {
	enc := 4 * ((n + 2) / 3)
	lines := (enc + 75) / 76
	if lines == 0 {
		return 0
	}
	return enc + 2*(lines-1)
}

func (s PlanSpec) poolEncLen(j int) int { return b64Len(s.poolRawLen(j)) }

// eligible is how many pool entries fit in a message of the given size.
func (s PlanSpec) eligible(size int) int {
	n := 0
	for j := range s.PoolSize {
		if s.poolEncLen(j)+messageOverhead <= size {
			n++
		}
	}
	return n
}

// PlanUser derives user i's plan from its own stream, so it does not depend
// on the user count, concurrency or order. The draws per message are fixed
// whatever the outcome, so a message's plan does not depend on whether the
// messages before it were eligible for an attachment.
func (s PlanSpec) PlanUser(i int) UserPlan {
	// #nosec G404 G115 -- deterministic test data; i is a non-negative index.
	rng := rand.New(rand.NewPCG(s.Seed, uint64(i)))
	up := UserPlan{Index: i, Email: s.email(i)}
	for j := range s.MPU {
		bucket := pick(rng, s.Sizes)
		size := max(int(float64(bucket)*(0.75+0.5*rng.Float64())), minMessageSize)
		folder := pick(rng, s.Folders)
		share := rng.Float64()
		choice := rng.IntN(math.MaxInt32)
		bodySeed := rng.Uint64()
		attach := -1
		if n := s.eligible(size); n > 0 && share < s.DedupShare {
			attach = choice % n
		}
		up.Messages = append(up.Messages, MessagePlan{
			User: i, Index: j, ID: s.messageID(i, j), Size: size,
			Folder: folder, Attach: attach, BodySeed: bodySeed,
		})
	}
	return up
}

// Plan is the whole plan; the run itself streams PlanUser.
func (s PlanSpec) Plan() []UserPlan {
	out := make([]UserPlan, s.Users)
	for i := range out {
		out[i] = s.PlanUser(i)
	}
	return out
}

// EffectiveDedupShare is the expected share of all messages that carry a
// pooled attachment: DedupShare times the chance that a message is large
// enough for the smallest pool entry (sizes spread uniformly over 75% to
// 125% of their bucket).
func (s PlanSpec) EffectiveDedupShare() float64 {
	threshold := float64(s.poolEncLen(0) + messageOverhead)
	total, fit := 0.0, 0.0
	for _, w := range s.Sizes {
		b := float64(w.Val)
		f := min(max((1.25*b-threshold)/(0.5*b), 0), 1)
		total += float64(w.Weight)
		fit += float64(w.Weight) * f
	}
	return s.DedupShare * fit / total
}

// AttachPool holds the shared attachments as encoded MIME bodies. It is
// filled at construction, so workers share it read-only.
type AttachPool struct {
	spec PlanSpec
	enc  [][]byte
}

func newAttachPool(s PlanSpec) *AttachPool {
	p := &AttachPool{spec: s, enc: make([][]byte, s.PoolSize)}
	for j := range p.enc {
		p.enc[j] = encodeAttachment(s, j)
	}
	return p
}

func encodeAttachment(s PlanSpec, j int) []byte {
	// #nosec G404 G115 -- deterministic test data; j is a non-negative index.
	rng := rand.New(rand.NewPCG(s.Seed, 1<<40+uint64(j)))
	raw := make([]byte, s.poolRawLen(j))
	for i := 0; i < len(raw); i += 8 {
		var w [8]byte
		binary.LittleEndian.PutUint64(w[:], rng.Uint64())
		copy(raw[i:], w[:])
	}
	flat := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	for len(flat) > 76 {
		b.WriteString(flat[:76])
		b.WriteString("\r\n")
		flat = flat[76:]
	}
	b.WriteString(flat)
	return []byte(b.String())
}

const textAlphabet = "abcdefghijklmnopqrstuvwxyz      "

func textBody(seed uint64, n int) string {
	// #nosec G404 -- deterministic test data, not a security boundary.
	rng := rand.New(rand.NewPCG(seed, 0))
	var b strings.Builder
	b.Grow(n + 2)
	col := 0
	for b.Len() < n {
		if col == 72 {
			b.WriteString("\r\n")
			col = 0
			continue
		}
		b.WriteByte(textAlphabet[rng.IntN(len(textAlphabet))])
		col++
	}
	return b.String()
}

// build renders a message. The same plan always gives the same bytes. The
// Message-ID is repeated in X-Loadgen-Id so a mailbox search can find a
// redelivered copy.
func (p *AttachPool) build(mp MessagePlan, rcpt string) []byte {
	s := p.spec
	const date = "Tue, 14 Nov 2023 22:13:20 +0000"
	var h strings.Builder
	fmt.Fprintf(&h, "From: %s\r\nTo: %s\r\nSubject: loadgen %d/%d\r\nDate: %s\r\n", s.sender(), rcpt, mp.User, mp.Index, date)
	fmt.Fprintf(&h, "Message-ID: %s\r\nX-Loadgen-Id: %s\r\nMIME-Version: 1.0\r\n", mp.ID, mp.ID)
	if mp.Attach < 0 {
		h.WriteString("Content-Type: text/plain; charset=us-ascii\r\n\r\n")
		return []byte(h.String() + textBody(mp.BodySeed, max(mp.Size-h.Len(), 64)) + "\r\n")
	}
	att := p.enc[mp.Attach]
	boundary := fmt.Sprintf("=_lg_%x", mp.BodySeed)
	fmt.Fprintf(&h, "Content-Type: multipart/mixed; boundary=\"%s\"\r\n\r\n", boundary)
	textHead := "--" + boundary + "\r\nContent-Type: text/plain; charset=us-ascii\r\n\r\n"
	attHead := fmt.Sprintf("\r\n--%s\r\nContent-Type: application/octet-stream; name=\"pool%02d.bin\"\r\n"+
		"Content-Disposition: attachment; filename=\"pool%02d.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\n", boundary, mp.Attach, mp.Attach)
	tail := "\r\n--" + boundary + "--\r\n"
	pad := max(mp.Size-h.Len()-len(textHead)-len(attHead)-len(att)-len(tail), 64)
	var out strings.Builder
	out.Grow(h.Len() + len(textHead) + pad + len(attHead) + len(att) + len(tail))
	out.WriteString(h.String())
	out.WriteString(textHead)
	out.WriteString(textBody(mp.BodySeed, pad))
	out.WriteString(attHead)
	out.Write(att)
	out.WriteString(tail)
	return []byte(out.String())
}
