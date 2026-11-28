package store

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-project/bongo/internal/i18n"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Attachment policy actions.
const (
	AttachmentActionStrip      = "strip"
	AttachmentActionQuarantine = "quarantine"
)

// Limits on one domain's attachment policy.
const (
	MaxAttachmentExtensions = 100
	MaxAttachmentMimeTypes  = 100
	maxAttachmentMimeLen    = 127
)

var (
	attachmentExtPattern  = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	attachmentMimePattern = regexp.MustCompile("^[a-z0-9][a-z0-9!#$&^_.+-]*/[a-z0-9][a-z0-9!#$&^_.+-]*$")
)

// AttachmentPolicyKey returns the Pebble key of a domain's attachment policy:
// /system/domain/{domain}/attachment_policy, an AttachmentPolicy protobuf.
// The key lies under /system/domain/{d}/, so PurgeDomainConfig removes it.
func AttachmentPolicyKey(domain string) []byte {
	return []byte("/system/domain/" + strings.ToLower(strings.TrimSpace(domain)) + "/attachment_policy")
}

// AttachmentPolicyFieldError names the form field a policy value was refused for.
type AttachmentPolicyFieldError struct {
	Field string // "extensions", "mime_types", "action" or "language"
	Msg   string
}

func (e *AttachmentPolicyFieldError) Error() string {
	return "attachment policy: " + e.Field + ": " + e.Msg
}

// NormalizeAttachmentPolicy validates p and rewrites its lists into the stored
// form (lower case, no leading dot, de-duplicated, sorted) so equal policies
// have equal bytes. It changes p only on success.
func NormalizeAttachmentPolicy(p *bongov1.AttachmentPolicy) error {
	if p == nil {
		return &AttachmentPolicyFieldError{Field: "action", Msg: "missing policy"}
	}
	if p.Action != AttachmentActionStrip && p.Action != AttachmentActionQuarantine {
		return &AttachmentPolicyFieldError{Field: "action", Msg: "must be strip or quarantine"}
	}
	if p.Language != "" && !i18n.SupportedLocale(p.Language) {
		return &AttachmentPolicyFieldError{Field: "language", Msg: "unsupported language"}
	}
	exts := make([]string, 0, len(p.BlockedExtensions))
	for _, e := range p.BlockedExtensions {
		e = strings.ToLower(strings.TrimSpace(e))
		e = strings.TrimPrefix(e, ".")
		if !attachmentExtPattern.MatchString(e) {
			return &AttachmentPolicyFieldError{Field: "extensions", Msg: "each extension must be 1 to 16 letters or digits"}
		}
		exts = append(exts, e)
	}
	slices.Sort(exts)
	exts = slices.Compact(exts)
	if len(exts) > MaxAttachmentExtensions {
		return &AttachmentPolicyFieldError{Field: "extensions", Msg: fmt.Sprintf("at most %d extensions", MaxAttachmentExtensions)}
	}
	types := make([]string, 0, len(p.BlockedMimeTypes))
	for _, m := range p.BlockedMimeTypes {
		m = strings.ToLower(strings.TrimSpace(m))
		if len(m) > maxAttachmentMimeLen || !attachmentMimePattern.MatchString(m) {
			return &AttachmentPolicyFieldError{Field: "mime_types", Msg: "each entry must be a type/subtype token"}
		}
		types = append(types, m)
	}
	slices.Sort(types)
	types = slices.Compact(types)
	if len(types) > MaxAttachmentMimeTypes {
		return &AttachmentPolicyFieldError{Field: "mime_types", Msg: fmt.Sprintf("at most %d MIME types", MaxAttachmentMimeTypes)}
	}
	p.BlockedExtensions = exts
	p.BlockedMimeTypes = types
	return nil
}

// AttachmentPolicyStore reads and writes per-domain attachment policies.
// Writes are one plain PUT through Raft.
type AttachmentPolicyStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewAttachmentPolicyStore creates an AttachmentPolicyStore.
func NewAttachmentPolicyStore(pebble *PebbleStore, applier RaftApplier) *AttachmentPolicyStore {
	return &AttachmentPolicyStore{pebble: pebble, applier: applier}
}

// Get returns the domain's policy, or (nil, nil) when none is set.
func (s *AttachmentPolicyStore) Get(domain string) (*bongov1.AttachmentPolicy, error) {
	data, err := s.pebble.Get(AttachmentPolicyKey(domain))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("attachment policy get %q: %w", domain, err)
	}
	var p bongov1.AttachmentPolicy
	if err := proto.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("attachment policy get %q: unmarshal: %w", domain, err)
	}
	return &p, nil
}

// Put validates, normalises and stores the policy, stamping updated_at and
// updated_by. A validation failure writes nothing and returns an
// *AttachmentPolicyFieldError.
func (s *AttachmentPolicyStore) Put(domain string, p *bongov1.AttachmentPolicy, updatedBy string) error {
	c := proto.Clone(p).(*bongov1.AttachmentPolicy)
	if err := NormalizeAttachmentPolicy(c); err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Unix()
	c.UpdatedBy = updatedBy
	data, err := proto.Marshal(c)
	if err != nil {
		return fmt.Errorf("attachment policy put %q: marshal: %w", domain, err)
	}
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: AttachmentPolicyKey(domain), Value: data}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("attachment policy put %q: raft: %w", domain, err)
	}
	return nil
}
