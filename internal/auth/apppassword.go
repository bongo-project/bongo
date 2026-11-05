package auth

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// MaxAppPasswordLabelLen is the longest label, in characters.
const MaxAppPasswordLabelLen = 64

// ErrInvalidAppPasswordLabel is returned for an empty, over-long or
// control-character label. The label is user input that reaches logs.
var ErrInvalidAppPasswordLabel = errors.New("invalid app password label")

// appPasswordGroups and appPasswordGroupLen describe the displayed shape: four
// groups of six letters. The first group is the public lookup id, the other
// three are the secret.
const (
	appPasswordGroups   = 4
	appPasswordGroupLen = 6
	appPasswordLen      = appPasswordGroups * appPasswordGroupLen
)

// maxLookupRetries bounds regeneration on a lookup id collision.
const maxLookupRetries = 10

// compareHash is a seam so tests can count bcrypt comparisons, which must be
// exactly one per protocol attempt on every branch.
var compareHash = bcrypt.CompareHashAndPassword

// AppPasswordRecords is the persistence the app password service needs. The
// store package's AppPasswordStore satisfies it.
type AppPasswordRecords interface {
	Put(userID string, rec *bongov1.AppPassword) error
	Get(userID, id string) (*bongov1.AppPassword, bool, error)
	List(userID string) ([]*bongov1.AppPassword, error)
	Delete(userID, id string) error
	Touch(userID, id string, at int64) error
}

var _ AppPasswordRecords = (*store.AppPasswordStore)(nil)

// AppPasswordInfo is what a caller may show or log about a credential: never
// the hash.
type AppPasswordInfo struct {
	ID         string
	Label      string
	CreatedAt  time.Time
	LastUsedAt time.Time // zero when never used; date granular
}

func infoOf(rec *bongov1.AppPassword) AppPasswordInfo {
	info := AppPasswordInfo{ID: rec.Id, Label: rec.Label, CreatedAt: time.Unix(rec.CreatedAt, 0).UTC()}
	if rec.LastUsedAt > 0 {
		info.LastUsedAt = time.Unix(rec.LastUsedAt, 0).UTC()
	}
	return info
}

// FormatAppPassword renders a canonical 24-letter app password as four
// hyphen-separated groups. It is the one display format.
func FormatAppPassword(canonical string) string {
	var b strings.Builder
	for i := 0; i < len(canonical); i += appPasswordGroupLen {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(canonical[i:min(i+appPasswordGroupLen, len(canonical))])
	}
	return b.String()
}

// MaskAppPassword renders the one part of a credential that is public, the
// lookup id, in the shape of a full app password with the secret groups hidden.
// It is what the list shows after the plaintext is gone.
func MaskAppPassword(id string) string {
	groups := []string{id}
	for range appPasswordGroups - 1 {
		groups = append(groups, strings.Repeat("*", appPasswordGroupLen))
	}
	return strings.Join(groups, "-")
}

// ParseAppPassword reads a typed app password. It accepts hyphens, spaces or no
// separator and folds ASCII case (phones auto-capitalise), and returns the
// public lookup id and the 18-letter secret. ok is false unless the input is
// exactly 24 letters of the alphabet after that, so attacker input reaches a
// store key only after this shape check.
func ParseAppPassword(s string) (id, secret string, ok bool) {
	var b [appPasswordLen]byte
	n := 0
	for i := range len(s) {
		c := s[i]
		if c == '-' || c == ' ' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if n == appPasswordLen || strings.IndexByte(store.AppPasswordAlphabet, c) < 0 {
			return "", "", false
		}
		b[n] = c
		n++
	}
	if n != appPasswordLen {
		return "", "", false
	}
	return string(b[:appPasswordGroupLen]), string(b[appPasswordGroupLen:]), true
}

func randomLetters(n int) (string, error) {
	max := big.NewInt(int64(len(store.AppPasswordAlphabet)))
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("reading random: %w", err)
		}
		out[i] = store.AppPasswordAlphabet[v.Int64()]
	}
	return string(out), nil
}

func validateAppPasswordLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" || !utf8.ValidString(label) || utf8.RuneCountInString(label) > MaxAppPasswordLabelLen {
		return "", ErrInvalidAppPasswordLabel
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return "", ErrInvalidAppPasswordLabel
		}
	}
	return label, nil
}

// AppPasswords creates, lists, revokes and verifies per-device protocol
// credentials. There is no cache: every verification reads the record, so a
// revocation takes effect on the next attempt.
type AppPasswords struct {
	records AppPasswordRecords
	logger  *slog.Logger
	now     func() time.Time
	// benignTouchErr marks a failed touch that is expected, such as the
	// not-leader error on a follower, and is logged at Debug.
	benignTouchErr func(error) bool

	// touching holds (user, lookup) pairs with a touch in flight, so
	// concurrent logins at a day boundary issue one command.
	touching sync.Map
	// touchWG lets tests wait for the detached touch.
	touchWG sync.WaitGroup
}

// NewAppPasswords creates the service. A nil logger discards logs.
func NewAppPasswords(records AppPasswordRecords, logger *slog.Logger) *AppPasswords {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &AppPasswords{records: records, logger: logger, now: time.Now}
}

// SetBenignTouchError sets the predicate for touch failures that are expected
// on some nodes. Wired by the agent, which knows the cluster error types.
func (p *AppPasswords) SetBenignTouchError(f func(error) bool) { p.benignTouchErr = f }

// Create makes a new credential for userID and returns its plaintext, shown
// once, and its metadata. The plaintext is never stored or logged.
func (p *AppPasswords) Create(userID, label string) (string, AppPasswordInfo, error) {
	label, err := validateAppPasswordLabel(label)
	if err != nil {
		return "", AppPasswordInfo{}, err
	}

	// The collision check is check-then-act: two concurrent creates for one
	// user are not serialised. The odds of both drawing the same id are about
	// 1 in 148 million per pair, and the later Put would replace the earlier.
	var id string
	for range maxLookupRetries {
		cand, err := randomLetters(appPasswordGroupLen)
		if err != nil {
			return "", AppPasswordInfo{}, err
		}
		_, exists, err := p.records.Get(userID, cand)
		if err != nil {
			return "", AppPasswordInfo{}, fmt.Errorf("checking lookup id: %w", err)
		}
		if !exists {
			id = cand
			break
		}
	}
	if id == "" {
		return "", AppPasswordInfo{}, errors.New("no free app password lookup id")
	}

	secret, err := randomLetters(appPasswordLen - appPasswordGroupLen)
	if err != nil {
		return "", AppPasswordInfo{}, err
	}
	// Same cost as the account hash, so a hit and a miss cost the same.
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), PasswordHashCost)
	if err != nil {
		return "", AppPasswordInfo{}, fmt.Errorf("hashing app password: %w", err)
	}
	rec := &bongov1.AppPassword{Id: id, Label: label, CreatedAt: p.now().Unix(), PasswordHash: hash}
	if err := p.records.Put(userID, rec); err != nil {
		return "", AppPasswordInfo{}, fmt.Errorf("storing app password: %w", err)
	}
	p.logger.Info("app password created", "user_id", userID, "label", label, "lookup", id)
	return FormatAppPassword(id + secret), infoOf(rec), nil
}

// List returns the user's credentials without their hashes.
func (p *AppPasswords) List(userID string) ([]AppPasswordInfo, error) {
	recs, err := p.records.List(userID)
	if err != nil {
		return nil, fmt.Errorf("listing app passwords: %w", err)
	}
	out := make([]AppPasswordInfo, 0, len(recs))
	for _, r := range recs {
		out = append(out, infoOf(r))
	}
	return out, nil
}

// Revoke deletes one credential. Idempotent. The id is checked for shape
// before any key is built.
func (p *AppPasswords) Revoke(userID, id string) error {
	if !store.ValidAppPasswordID(id) {
		return store.ErrInvalidAppPasswordID
	}
	// The label is read for the log only; a read failure does not stop the
	// revoke.
	rec, existed, err := p.records.Get(userID, id)
	label := ""
	if err != nil {
		// The record may exist, so the revoke is still logged, without a label.
		existed = true
	} else if existed {
		label = rec.Label
	}
	if err := p.records.Delete(userID, id); err != nil {
		return fmt.Errorf("revoking app password: %w", err)
	}
	if existed {
		p.logger.Info("app password revoked", "user_id", userID, "label", label, "lookup", id)
	}
	return nil
}

// RevokeAll deletes every credential of userID and returns how many it
// deleted. It is one plain Delete per credential, not a composite command, so
// it is not atomic: it stops at the first failure and returns the count so far
// with the error. Credentials already deleted stay deleted, the rest still
// work, and running it again finishes the job because Revoke is idempotent. A
// credential created after the List below survives; closing that gap would
// need a composite Raft command, which a node on an earlier binary would store
// as a literal key.
func (p *AppPasswords) RevokeAll(userID string) (int, error) {
	infos, err := p.List(userID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, info := range infos {
		if err := p.Revoke(userID, info.ID); err != nil {
			return n, fmt.Errorf("revoking app password %s after %d: %w", info.ID, n, err)
		}
		n++
	}
	return n, nil
}

// recordUse notes a successful authentication with rec. last_used_at is date
// granular and written at most once per credential per day. The write is
// best-effort: a failure leaves the displayed last-used date stale and
// nothing else, so it runs detached (a leaderless leader must not add its
// timeout to a login) and is only logged. On a follower it fails with
// not-leader, which is expected and logged at Debug.
func (p *AppPasswords) recordUse(userID string, rec *bongov1.AppPassword) {
	now := p.now().Unix()
	if rec.LastUsedAt >= store.DayStart(now) {
		return
	}
	key := userID + "/" + rec.Id
	if _, busy := p.touching.LoadOrStore(key, struct{}{}); busy {
		return
	}
	firstUse := rec.LastUsedAt == 0
	label, id := rec.Label, rec.Id
	p.touchWG.Add(1)
	go func() {
		defer p.touchWG.Done()
		defer p.touching.Delete(key)
		if err := p.records.Touch(userID, id, now); err != nil {
			if p.benignTouchErr != nil && p.benignTouchErr(err) {
				p.logger.Debug("app password last-used not recorded", "user_id", userID, "lookup", id, "error", err)
				return
			}
			p.logger.Warn("app password last-used not recorded", "user_id", userID, "lookup", id, "error", err)
			return
		}
		if firstUse {
			p.logger.Info("app password first used", "user_id", userID, "label", label, "lookup", id)
		}
	}()
}

// Wait blocks until in-flight last-used writes finish. Tests use it to avoid
// sleeping; the agent does not call it at shutdown, where a lost last-used
// write is harmless.
func (p *AppPasswords) Wait() { p.touchWG.Wait() }
