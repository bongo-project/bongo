package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
	"unicode"

	"github.com/bongo-project/bongo/internal/icalevent"
	"github.com/bongo-project/bongo/internal/netx"
	"github.com/bongo-project/bongo/internal/store"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// Calendar subscriptions: a leader-only worker keeps each subscribed calendar
// in step with its external ICS feed.
//
// Schedule. Every subscriptionTick the worker looks for subscriptions whose
// next_refresh_at has passed; a new one has 0 and is due at once. After a
// success the next refresh is 6 hours away, or the feed's own REFRESH-INTERVAL
// or X-PUBLISHED-TTL when it gives one, but never sooner than 1 hour and never
// later than 24 hours. A failure is retried after 1 hour. The interval is not
// a config key.
//
// Fetching. One subscription at a time, every request under the worker's
// context so shutdown does not wait out a 30-second fetch per subscription.
// The request is conditional (If-None-Match, If-Modified-Since); a 304 only
// updates the state record.
//
// Applying a feed. The feed is parsed completely before anything is written:
// an oversize body, a decode error, a truncated body, an empty feed or one
// over 5000 events fails the refresh, records last_error and leaves the
// calendar exactly as it was. Only a feed that parsed whole is applied, and
// it is applied by UID: events new to the calendar are created, changed ones
// updated, and ones the feed no longer lists deleted, each a separate Raft
// write (creates and updates first, deletes last). Events are stored under a
// guid derived from (calendar, UID), never the UID itself, and only events
// whose stored calendar is this one are ever deleted.
//
// Partial failure. These writes are not atomic. A failure part-way, for
// example a leadership change, leaves some events old and some new; the pass
// stops, records the error and the next refresh, which compares the whole feed
// against the whole calendar again, writes what is still different. Nothing
// is lost that the feed still lists. Validators are only saved after a full
// apply, so a half-applied feed is never skipped by a 304.
//
// State record. The calendar record is re-read right before each state write
// and only the subscription message is changed (CalendarStore.PatchSubscription),
// so a rename made during the fetch survives. If the calendar was deleted in
// the meantime nothing is written back, and any events this pass created for
// it are removed.
//
// Quota. The refresh does not check the domain storage quota: a subscription
// is bounded by 5000 events and 20 subscriptions per user, and the quota
// recompute counts these events like any others.
//
// Secrecy. The feed URL is a secret. Logs and last_error carry the host only.

const (
	subscriptionTick            = 2 * time.Minute
	subscriptionDefaultInterval = 6 * time.Hour
	subscriptionMinInterval     = time.Hour
	subscriptionMaxInterval     = 24 * time.Hour
	subscriptionRetryInterval   = time.Hour
	subscriptionMaxErrorLen     = 300
)

// feedFetcher is what the worker needs of the HTTP client; *netx.Client
// satisfies it.
type feedFetcher interface {
	Fetch(ctx context.Context, rawURL string, v netx.Validators) (*netx.Result, error)
}

// subscriptionRefresher is one set of dependencies for a refresh pass.
type subscriptionRefresher struct {
	users     func() ([]string, error)
	calendars *store.CalendarStore
	events    *store.EventStore
	fetcher   feedFetcher
	logger    *slog.Logger
}

// errStopPass ends a pass: the node lost leadership or Raft refused a write,
// so the remaining subscriptions are left to the next leader or tick.
var errStopPass = errors.New("stopping the subscription pass")

// runCalendarSubscriptions runs a pass on every tick until ctx ends.
func (a *Agent) runCalendarSubscriptions(ctx context.Context, interval time.Duration, isLeader func() bool, fetcher feedFetcher) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	r := a.newSubscriptionRefresher(fetcher)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pass(ctx, isLeader, time.Now())
		}
	}
}

func (a *Agent) newSubscriptionRefresher(fetcher feedFetcher) *subscriptionRefresher {
	return &subscriptionRefresher{
		users: func() ([]string, error) {
			return a.Metadata.System.ListUserIDs()
		},
		calendars: a.Calendars,
		events:    a.Events,
		fetcher:   fetcher,
		logger:    a.logger,
	}
}

// pass refreshes every due subscription and returns how many it attempted.
// It does nothing on a node that is not the leader.
func (r *subscriptionRefresher) pass(ctx context.Context, isLeader func() bool, now time.Time) int {
	if !isLeader() {
		return 0
	}
	ids, err := r.users()
	if err != nil {
		r.logger.Warn("calendar-subscriptions: listing users failed", "error", err)
		return 0
	}
	attempted := 0
	for _, userID := range ids {
		subs, err := r.calendars.ListSubscribed(userID)
		if err != nil {
			r.logger.Warn("calendar-subscriptions: listing subscriptions failed", "user", userID, "error", err)
			continue
		}
		for _, cal := range subs {
			if ctx.Err() != nil || !isLeader() {
				return attempted
			}
			if cal.Subscription.NextRefreshAt > now.Unix() {
				continue
			}
			attempted++
			if err := r.refresh(ctx, userID, cal, now); errors.Is(err, errStopPass) {
				return attempted
			}
		}
	}
	return attempted
}

// refresh fetches and applies one subscription. It returns errStopPass when
// the pass must end; any other failure is recorded on the calendar.
func (r *subscriptionRefresher) refresh(ctx context.Context, userID string, cal *bongov1.Calendar, now time.Time) error {
	sub := cal.Subscription
	host := netx.HostOf(sub.SourceUrl)

	res, err := r.fetcher.Fetch(ctx, sub.SourceUrl, netx.Validators{ETag: sub.Etag, LastModified: sub.LastModified})
	if err != nil {
		if ctx.Err() != nil {
			return errStopPass // shutting down: not a feed failure
		}
		return r.fail(userID, cal, host, now, err)
	}
	if res.NotModified {
		r.logger.Debug("calendar-subscriptions: not modified", "user", userID, "calendar", cal.Guid, "host", host)
		return r.succeed(userID, cal, host, now, nil, sub.Etag, sub.LastModified, int(sub.EventCount))
	}

	feed, err := icalevent.ParseFeed(res.Body, store.MaxEventsPerSubscription)
	if err != nil {
		return r.fail(userID, cal, host, now, err)
	}
	if err := r.apply(userID, cal, feed); err != nil {
		if errors.Is(err, store.ErrSubscriptionGone) {
			return r.stateWritten(userID, cal, host, err)
		}
		// A write failed part-way: record it and end the pass. The usual
		// cause is a lost leadership, and the next refresh corrects the rest.
		_ = r.fail(userID, cal, host, now, err)
		return errStopPass
	}
	return r.succeed(userID, cal, host, now, feed, res.ETag, res.LastModified, len(feed.Events))
}

// apply replaces the calendar's events by UID. See the file comment for the
// order and the partial-failure semantics.
func (r *subscriptionRefresher) apply(userID string, cal *bongov1.Calendar, feed *icalevent.Feed) error {
	all, _, err := r.events.ListAll(userID, 0, 0)
	if err != nil {
		return fmt.Errorf("listing events: %w", err)
	}
	byGUID := make(map[string]*bongov1.Event, len(all))
	for _, e := range all {
		byGUID[e.Guid] = e
	}

	// The calendar may have been deleted while the feed was fetched; do not
	// create events for a calendar that is gone.
	if ok, err := r.calendars.CalendarIsSubscribed(userID, cal.Guid); err != nil || !ok {
		if err != nil {
			return fmt.Errorf("checking calendar: %w", err)
		}
		return store.ErrSubscriptionGone
	}

	wanted := make(map[string]bool, len(feed.Events))
	for _, fe := range feed.Events {
		guid := store.SubscriptionEventGUID(cal.Guid, fe.UID)
		wanted[guid] = true
		existing := byGUID[guid]
		switch {
		case existing == nil:
			ev := fe.Event
			ev.Guid, ev.CalendarId, ev.IcalRaw = guid, cal.Guid, fe.Raw
			if _, err := r.events.Create(userID, ev); err != nil {
				return fmt.Errorf("creating event: %w", err)
			}
		case existing.CalendarId != cal.Guid:
			// Cannot happen with a 128-bit derived guid; if it ever does,
			// leave the other calendar's event alone.
			r.logger.Warn("calendar-subscriptions: guid belongs to another calendar", "user", userID, "calendar", cal.Guid)
		case !bytes.Equal(existing.IcalRaw, fe.Raw):
			ev := fe.Event
			ev.Guid, ev.CalendarId, ev.IcalRaw = guid, cal.Guid, fe.Raw
			ev.CreatedAt = existing.CreatedAt
			if err := r.events.Update(userID, guid, ev); err != nil {
				return fmt.Errorf("updating event: %w", err)
			}
		}
	}
	for _, e := range all {
		if e.CalendarId != cal.Guid || wanted[e.Guid] {
			continue
		}
		if err := r.events.Delete(userID, e.Guid); err != nil {
			return fmt.Errorf("deleting event: %w", err)
		}
	}
	return nil
}

// succeed records a successful refresh. feed is nil for a 304.
func (r *subscriptionRefresher) succeed(userID string, cal *bongov1.Calendar, host string, now time.Time, feed *icalevent.Feed, etag, lastModified string, count int) error {
	interval := subscriptionDefaultInterval
	if feed != nil && feed.RefreshHint > 0 {
		interval = min(max(feed.RefreshHint, subscriptionMinInterval), subscriptionMaxInterval)
	}
	err := r.calendars.PatchSubscription(userID, cal.Guid, func(s *bongov1.CalendarSubscription) {
		s.Etag, s.LastModified = etag, lastModified
		s.LastAttemptAt, s.LastSuccessAt = now.Unix(), now.Unix()
		s.LastError = ""
		s.NextRefreshAt = now.Add(interval).Unix()
		s.EventCount = int32(count) // #nosec G115 -- bounded by MaxEventsPerSubscription
	})
	return r.stateWritten(userID, cal, host, err)
}

// fail records a failed refresh. The error text is bounded and stripped of
// control characters; netx and the parser put no URL in it.
func (r *subscriptionRefresher) fail(userID string, cal *bongov1.Calendar, host string, now time.Time, cause error) error {
	msg := failureMessage(cause)
	r.logger.Warn("calendar-subscriptions: refresh failed", "user", userID, "calendar", cal.Guid, "host", host, "error", msg)
	err := r.calendars.PatchSubscription(userID, cal.Guid, func(s *bongov1.CalendarSubscription) {
		s.LastAttemptAt = now.Unix()
		s.LastError = msg
		s.NextRefreshAt = now.Add(subscriptionRetryInterval).Unix()
	})
	return r.stateWritten(userID, cal, host, err)
}

// errConnect is the one message a user sees for every connection-level
// failure. The refusal text, a resolved address or a DNS error would tell an
// authenticated user which internal names exist and where they point, turning
// subscriptions into a probe of the private network.
const errConnect = "could not connect to the feed host"

// failureMessage turns a refresh error into the text stored in last_error and
// shown on the settings card. Dial refusals, DNS and socket errors and
// timeouts collapse to errConnect; parse, status, size and cap messages carry
// nothing about the network and stay as they are.
func failureMessage(err error) string {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case errors.Is(err, netx.ErrForbiddenAddress),
		errors.Is(err, netx.ErrForbiddenPort),
		errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &netErr),
		errors.As(err, &dnsErr):
		return errConnect
	}
	return sanitiseError(err.Error())
}

// stateWritten interprets the result of a state write. A calendar deleted in
// the meantime is not an error: its events, which this pass may have just
// created, are removed so none are orphaned.
func (r *subscriptionRefresher) stateWritten(userID string, cal *bongov1.Calendar, host string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrSubscriptionGone):
		r.logger.Info("calendar-subscriptions: calendar deleted during refresh", "user", userID, "calendar", cal.Guid, "host", host)
		r.purgeEvents(userID, cal.Guid)
		return nil
	default:
		r.logger.Warn("calendar-subscriptions: state write failed", "user", userID, "calendar", cal.Guid, "host", host, "error", err)
		return errStopPass
	}
}

// purgeEvents deletes the events of a calendar that no longer exists. Best
// effort: a failure is logged and the events stay until a later purge.
func (r *subscriptionRefresher) purgeEvents(userID, calID string) {
	all, _, err := r.events.ListAll(userID, 0, 0)
	if err != nil {
		r.logger.Warn("calendar-subscriptions: listing events to purge failed", "user", userID, "error", err)
		return
	}
	for _, e := range all {
		if e.CalendarId != calID {
			continue
		}
		if err := r.events.Delete(userID, e.Guid); err != nil {
			r.logger.Warn("calendar-subscriptions: purge failed", "user", userID, "calendar", calID, "error", err)
			return
		}
	}
}

func sanitiseError(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > subscriptionMaxErrorLen {
		s = strings.ToValidUTF8(s[:subscriptionMaxErrorLen], "") + "..."
	}
	return s
}
