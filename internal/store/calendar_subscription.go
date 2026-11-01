package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Limits of calendar subscriptions (external ICS feeds).
const (
	MaxSubscriptionsPerUser   = 20
	MaxEventsPerSubscription  = 5000
	subscriptionEventGUIDBase = 32
)

// ErrSubscriptionGone is returned by PatchSubscription when the calendar no
// longer exists or is no longer a subscription. A caller must stop: writing
// state back would resurrect a calendar the user deleted.
var ErrSubscriptionGone = errors.New("subscription calendar is gone")

// IsSubscribed reports whether cal mirrors an external feed. Such a calendar
// is read-only for everyone, its owner included.
func IsSubscribed(cal *bongov1.Calendar) bool {
	return cal != nil && cal.Subscription != nil && cal.Subscription.SourceUrl != ""
}

// CalendarIsSubscribed reports whether the user's calendar calID is a
// subscription. An empty ID is the default calendar, which never is. A
// missing calendar is not subscribed; a read error is returned so a caller
// can refuse the write rather than guess.
func (s *CalendarStore) CalendarIsSubscribed(userID, calID string) (bool, error) {
	if calID == "" {
		return false, nil
	}
	data, err := s.pebble.Get(calendarKey(userID, calID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("reading calendar: %w", err)
	}
	var cal bongov1.Calendar
	if err := proto.Unmarshal(data, &cal); err != nil {
		return false, fmt.Errorf("unmarshaling calendar: %w", err)
	}
	return IsSubscribed(&cal), nil
}

// ListSubscribed returns the user's subscription calendars.
func (s *CalendarStore) ListSubscribed(userID string) ([]*bongov1.Calendar, error) {
	all, err := s.List(userID)
	if err != nil {
		return nil, err
	}
	var out []*bongov1.Calendar
	for _, c := range all {
		if IsSubscribed(c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// PatchSubscription changes the refresh state of a subscription and nothing
// else. It runs inside CalendarStore.Mutate, the per-calendar read-modify-write,
// so a rename, a recolour or a publish-token regeneration made during a
// 30-second fetch is neither undone by the refresh nor does the refresh write
// back a revoked token: fn sees the record as it is at the moment of writing
// and only the subscription message is handed to it.
//
// It returns ErrSubscriptionGone when the calendar was deleted (or stopped
// being a subscription) in the meantime.
func (s *CalendarStore) PatchSubscription(userID, guid string, fn func(sub *bongov1.CalendarSubscription)) error {
	err := s.Mutate(userID, guid, func(cal *bongov1.Calendar) error {
		if !IsSubscribed(cal) {
			return ErrSubscriptionGone
		}
		fn(cal.Subscription)
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return ErrSubscriptionGone
	}
	return err
}

// SubscriptionEventGUID is the key an event of a feed is stored under: derived
// from the calendar and the feed's UID. The raw UID is never used. A guid is
// per user, so a feed that chose UIDs freely could otherwise overwrite the
// user's own events or those of another subscription.
func SubscriptionEventGUID(calID, uid string) string {
	sum := sha256.Sum256([]byte(calID + "\x00" + uid))
	return "sub-" + hex.EncodeToString(sum[:])[:subscriptionEventGUIDBase]
}
