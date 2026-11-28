// Package attachpolicy provides the pipeline stage that enforces a domain's
// attachment policy on inbound mail.
//
// The policy that applies is read from Pebble for every message and never
// cached, so a change takes effect on the next message on every node. Which
// domain's policy applies is decided per recipient: the recipient is resolved
// to a local user and the policy of that user's primary-address domain is
// used, so an alias, a catch-all, an alias domain and a group copy all meet
// the policy of the domain whose administrator manages the user. An address
// that does not resolve falls back to the recipient's own domain, mapped
// through alias_of when it is an alias domain; a domain that is not hosted has
// no policy. A store error on any of these reads fails the stage, so the
// message is held rather than delivered unchecked.
//
// Stripping stores a new blob and points the envelope at it. The original blob
// is left alone: only the blob GC deletes, and a quarantined message keeps its
// original so an admin release delivers what arrived.
package attachpolicy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bongo-project/bongo/internal/antispam"
	"github.com/bongo-project/bongo/internal/attpolicy"
	"github.com/bongo-project/bongo/internal/pipeline"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// StageName is the stage identifier used in config, flags and stage results.
const StageName = "attachment_policy"

// noticeID is the message id of the text that replaces a stripped part.
const noticeID = "attachment_policy.notice"

// fallbackNotice is used when the translator has no text for noticeID.
const fallbackNotice = "The attachment \"%s\" was removed by the attachment policy of %s (blocked: %s)."

// Quarantine reasons that do not come from attpolicy.
const reasonTooLarge = "message_too_large"

// PolicySource reads a domain's policy; (nil, nil) means none is set.
type PolicySource interface {
	Get(domain string) (*bongov1.AttachmentPolicy, error)
}

// BlobPutter stores content-addressed bytes.
type BlobPutter interface {
	Put(data []byte) (string, error)
}

// Translator renders a message id for a locale with template data.
type Translator func(locale, id string, data map[string]interface{}) string

// Deps are the stage's collaborators.
type Deps struct {
	Policies PolicySource
	Blobs    BlobPutter
	// Resolve maps an envelope recipient address to a local user id. An
	// address that is not a live local user is an error wrapping nothing or
	// wrapping store.ErrRecipientUnavailable (as store.RecipientResolver
	// reports it); any other error is a store failure and fails the stage.
	Resolve func(email string) (string, error)
	// UserDomain returns the domain of a user's primary address. An error
	// other than store.ErrNotFound fails the stage.
	UserDomain func(userID string) (string, error)
	// PolicyDomain maps a domain to the domain whose policy applies (an alias
	// domain to its target) and reports whether the domain is hosted here.
	// An error fails the stage.
	PolicyDomain func(domain string) (target string, hosted bool, err error)
	Translate    Translator
	// MaxSize bounds how much of a message is read; a larger message is
	// quarantined. Zero means 25 MiB.
	MaxSize int64
	Logger  *slog.Logger
}

// Processor implements pipeline.Processor.
type Processor struct {
	d          Deps
	warnedNone atomic.Bool
}

// New returns the stage. A Deps with no policy source or no blob store makes
// the stage pass every message (logged once).
func New(d Deps) *Processor {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.MaxSize <= 0 {
		d.MaxSize = 25 << 20
	}
	return &Processor{d: d}
}

// Name returns the stage identifier.
func (p *Processor) Name() string { return StageName }

type applied struct {
	domain string
	pol    *bongov1.AttachmentPolicy
}

// Process applies the policy of each recipient's domain to the message.
func (p *Processor) Process(_ context.Context, env *bongov1.QueueEnvelope, msg io.Reader) (*bongov1.StageResult, error) {
	start := time.Now()
	result := func(action string, reasons ...string) *bongov1.StageResult {
		return &bongov1.StageResult{
			Stage:       StageName,
			Action:      action,
			Reasons:     reasons,
			ProcessedAt: time.Now().Unix(),
			DurationMs:  pipeline.ClampInt32(time.Since(start).Milliseconds()),
		}
	}
	if p.d.Policies == nil || p.d.Blobs == nil {
		if p.warnedNone.CompareAndSwap(false, true) {
			p.d.Logger.Warn("attachment-policy: no policy store or blob store; attachments are not checked")
		}
		return result("pass"), nil
	}

	pols, err := p.policiesFor(env)
	if err != nil {
		return nil, fmt.Errorf("attachment policy: %w", err)
	}
	if len(pols) == 0 {
		return result("pass"), nil
	}

	var exts, types []string
	quarantine := false
	lang, domain := "", ""
	for _, a := range pols {
		exts = append(exts, a.pol.BlockedExtensions...)
		types = append(types, a.pol.BlockedMimeTypes...)
		if a.pol.Action != store.AttachmentActionStrip {
			quarantine = true
		}
		if domain == "" {
			domain = a.domain
		}
		if lang == "" {
			lang = a.pol.Language
		}
	}
	pol := attpolicy.NewPolicy(exts, types)
	if pol.Empty() {
		return result("pass"), nil
	}

	raw, err := io.ReadAll(io.LimitReader(msg, p.d.MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("attachment policy: read message: %w", err)
	}
	if int64(len(raw)) > p.d.MaxSize {
		p.d.Logger.Info("attachment-policy: message too large to check, quarantined", "id", env.Id, "domain", domain)
		return result("quarantine", reasonTooLarge), nil
	}

	res := attpolicy.Evaluate(raw, pol, !quarantine, p.notice(lang, domain))
	switch {
	case res.Quarantine:
		p.d.Logger.Info("attachment-policy: message quarantined",
			"id", env.Id, "domain", domain, "reason", res.Reason, "rules", res.Rules, "parts", res.Parts)
		return result("quarantine", append([]string{res.Reason}, res.Rules...)...), nil
	case res.Out == nil:
		return result("pass"), nil
	}

	// DKIM is judged on the message as it arrived: the stripped copy can no
	// longer match its body hash, and deliver reads the verdict from here.
	dkimResult, dkimDomain, _ := antispam.VerifyDKIM(raw)
	hash, err := p.d.Blobs.Put(res.Out)
	if err != nil {
		return nil, fmt.Errorf("attachment policy: store stripped message: %w", err)
	}
	env.BlobHash = hash
	p.d.Logger.Info("attachment-policy: attachments stripped",
		"id", env.Id, "domain", domain, "rules", res.Rules, "parts", res.Parts)
	r := result("strip", res.Rules...)
	r.DkimResult, r.DkimDomain = dkimResult, dkimDomain
	return r, nil
}

// notice returns the function that writes the replacement text.
func (p *Processor) notice(lang, domain string) attpolicy.NoticeFunc {
	return func(name, rule string) string {
		if p.d.Translate != nil {
			if s := p.d.Translate(lang, noticeID, map[string]interface{}{"Name": name, "Domain": domain, "Rule": rule}); s != "" && s != noticeID {
				return s
			}
		}
		return fmt.Sprintf(fallbackNotice, name, domain, rule)
	}
}

// policiesFor returns the enabled policies that apply to the envelope's
// recipients, one per distinct domain.
func (p *Processor) policiesFor(env *bongov1.QueueEnvelope) ([]applied, error) {
	var out []applied
	seen := map[string]bool{}
	for _, rcpt := range env.GetRecipients() {
		domain, ok, err := p.domainOf(rcpt)
		if err != nil {
			return nil, err
		}
		if !ok || seen[domain] {
			continue
		}
		seen[domain] = true
		pol, err := p.d.Policies.Get(domain)
		if err != nil {
			return nil, fmt.Errorf("read policy for %s: %w", domain, err)
		}
		if pol != nil && pol.Enabled {
			out = append(out, applied{domain: domain, pol: pol})
		}
	}
	slices.SortFunc(out, func(a, b applied) int { return strings.Compare(a.domain, b.domain) })
	return out, nil
}

// domainOf names the domain whose policy applies to one recipient. ok is
// false when no policy can apply. A store failure is returned, never treated
// as "no policy": the caller fails the stage and the message is held.
func (p *Processor) domainOf(rcpt string) (domain string, ok bool, err error) {
	rcpt = strings.ToLower(strings.Trim(strings.TrimSpace(rcpt), "<>"))
	if p.d.Resolve != nil && p.d.UserDomain != nil {
		uid, rerr := p.d.Resolve(rcpt)
		switch {
		case rerr != nil && !recipientMiss(rerr):
			return "", false, fmt.Errorf("resolve recipient: %w", rerr)
		case rerr == nil && uid != "":
			d, derr := p.d.UserDomain(uid)
			switch {
			case derr == nil:
				domain = d
			case !errors.Is(derr, store.ErrNotFound):
				return "", false, fmt.Errorf("user domain: %w", derr)
			}
		}
	}
	if domain == "" {
		at := strings.LastIndexByte(rcpt, '@')
		if at < 0 || at == len(rcpt)-1 {
			return "", false, nil
		}
		domain = rcpt[at+1:]
	}
	if p.d.PolicyDomain != nil {
		target, hosted, perr := p.d.PolicyDomain(domain)
		if perr != nil {
			return "", false, fmt.Errorf("policy domain of %s: %w", domain, perr)
		}
		if !hosted {
			return "", false, nil
		}
		domain = target
	}
	return domain, true, nil
}

// recipientMiss reports whether a Resolve error only says the address is not
// a live local user. The resolver refuses an unknown, empty or reserved
// address with an error that wraps nothing, and an unavailable or group
// address with ErrRecipientUnavailable; a store failure it reports wraps the
// cause. Anything else is treated as a failure, the fail-closed side.
func recipientMiss(err error) bool {
	return errors.Is(err, store.ErrRecipientUnavailable) || errors.Unwrap(err) == nil
}
