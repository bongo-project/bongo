package store

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// The per-user feature switches an admin can turn off. A name in
// UserProfile.disabled_features that is not listed here is ignored when
// enforcing and kept when an admin edits the list, because a newer node may
// have written it.
const (
	FeatureIMAP       = "imap"
	FeaturePOP3       = "pop3"
	FeatureSubmission = "submission"
	FeatureDAV        = "dav"
	FeatureWeb        = "web"
	FeatureForwarding = "forwarding"
	FeatureRules      = "rules"
	FeatureVacation   = "vacation"
)

// Bounds for the per-user send limits.
const (
	MaxMessagesPerMinuteLimit = 10000
	MaxRecipientsPerHourLimit = 100000
	// MaxRecipientsCeiling is the largest per-message recipient cap an admin
	// can set for one user, and the cap the submission listeners accept
	// before the session applies the effective one.
	MaxRecipientsCeiling = 1000
)

// ErrInvalidSendLimits is returned by ValidateSendLimits.
var ErrInvalidSendLimits = errors.New("invalid send limits")

// KnownFeatures lists every switch in display order.
func KnownFeatures() []string {
	return []string{
		FeatureIMAP, FeaturePOP3, FeatureSubmission, FeatureDAV,
		FeatureWeb, FeatureForwarding, FeatureRules, FeatureVacation,
	}
}

// KnownFeature reports whether name is a switch this binary enforces.
func KnownFeature(name string) bool {
	return slices.Contains(KnownFeatures(), name)
}

// FeatureDisabled reports whether f is switched off for the profile. A nil
// profile disables nothing; callers deal with a missing account themselves.
func FeatureDisabled(p *bongov1.UserProfile, f string) bool {
	if p == nil {
		return false
	}
	return slices.Contains(p.DisabledFeatures, f)
}

// EffectiveLimit returns the per-user value when it is above zero and the
// global value otherwise. Zero therefore means "use the global value": a
// per-user setting can be stricter or looser than the global one but never
// means unlimited.
func EffectiveLimit(perUser uint32, global int) int {
	if perUser > 0 {
		return int(perUser)
	}
	return global
}

// ValidateSendLimits checks the bounds of a per-user limit set. A nil value is
// valid (everything global).
func ValidateSendLimits(l *bongov1.SendLimits) error {
	if l == nil {
		return nil
	}
	if l.MessagesPerMinute > MaxMessagesPerMinuteLimit {
		return fmt.Errorf("%w: messages per minute above %d", ErrInvalidSendLimits, MaxMessagesPerMinuteLimit)
	}
	if l.RecipientsPerHour > MaxRecipientsPerHourLimit {
		return fmt.Errorf("%w: recipients per hour above %d", ErrInvalidSendLimits, MaxRecipientsPerHourLimit)
	}
	if l.MaxRecipients > MaxRecipientsCeiling {
		return fmt.Errorf("%w: max recipients above %d", ErrInvalidSendLimits, MaxRecipientsCeiling)
	}
	if l.RecipientsPerHour > 0 && l.MaxRecipients > 0 && l.RecipientsPerHour < l.MaxRecipients {
		return fmt.Errorf("%w: recipients per hour below max recipients", ErrInvalidSendLimits)
	}
	return nil
}

// FeatureLookup answers whether a feature is switched off for a user, from
// the stored profile. It fails closed: when the profile is missing (the user
// has gone) or cannot be read, the answer is true, so nothing the switch
// guards is applied. The error is for logging only.
type FeatureLookup interface {
	FeatureDisabled(userID, feature string) (bool, error)
}

// ProfileFeatures is the FeatureLookup over the Pebble profile.
type ProfileFeatures struct {
	get func(key []byte) ([]byte, error)
}

// NewProfileFeatures reads profiles through get (a PebbleStore's Get).
func NewProfileFeatures(get func(key []byte) ([]byte, error)) *ProfileFeatures {
	return &ProfileFeatures{get: get}
}

// FeatureDisabled implements FeatureLookup.
func (p *ProfileFeatures) FeatureDisabled(userID, feature string) (bool, error) {
	data, err := p.get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return true, nil
		}
		return true, fmt.Errorf("reading profile: %w", err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return true, fmt.Errorf("parsing profile: %w", err)
	}
	return FeatureDisabled(&profile, feature), nil
}

// ReadSendLimits returns the per-user send limits stored on userID's profile
// (never nil). A missing or unreadable profile is an error, so a caller that
// must not send on a guess can fail closed.
func ReadSendLimits(get func(key []byte) ([]byte, error), userID string) (*bongov1.SendLimits, error) {
	data, err := get([]byte("/user/" + userID + "/profile"))
	if err != nil {
		return nil, fmt.Errorf("reading profile: %w", err)
	}
	var profile bongov1.UserProfile
	if err := proto.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parsing profile: %w", err)
	}
	if profile.SendLimits == nil {
		return &bongov1.SendLimits{}, nil
	}
	return profile.SendLimits, nil
}
