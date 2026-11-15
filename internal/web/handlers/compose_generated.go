package handlers

import (
	"bytes"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// splitLocal reduces each recipient to its bare address and splits them by
// whether they resolve to a local account. The To and Cc fields may carry a
// display name or angle brackets ("Alice <a@b.com>"), which must not leak into
// the SMTP envelope or the MX-lookup domain ("b.com>"). Blanks are dropped.
//
// A group address is a third class: it is neither local nor external. The
// resolver's group error is kept for that, and a group sent down the external
// path would be queued, relayed to this server's own MX and counted against the
// external budget and the server's own IP.
func (h *ComposeHandler) splitRecipients(rcpts []string) (local, external []string, groups []composeGroup) {
	for _, rcpt := range rcpts {
		rcpt = normalizeRecipient(rcpt)
		if rcpt == "" {
			continue
		}
		if id, ok := h.groupOf(rcpt); ok {
			groups = append(groups, composeGroup{addr: rcpt, id: id})
		} else if h.resolveLocalUser(rcpt) != "" {
			local = append(local, rcpt)
		} else {
			external = append(external, rcpt)
		}
	}
	return local, external, groups
}

// localEnvelope is the spool entry for one local recipient. It is Trusted with
// AuthUserId set to the author: compose is the second source of Trusted besides
// smtp.Session.inboundMeta, and AuthUserId comes only from the authenticated
// session, never from form input. blobHash names the whole RFC 5322 message.
func localEnvelope(id, userID, from, rcpt, blobHash string) *bongov1.QueueEnvelope {
	return &bongov1.QueueEnvelope{
		Id:         id,
		Sender:     from,
		Recipients: []string{rcpt},
		ReceivedAt: time.Now().Unix(),
		BlobHash:   blobHash,
		Trusted:    true,
		AuthUserId: userID,
	}
}

// outboundMessage is the queue entry for the external recipients of a message
// sent by userID. The return-path follows from, and the origin is local so the
// relay signs for the From domain.
func outboundMessage(id, userID, from string, rcpts []string, blobHash, messageID string) *bongov1.OutboundMessage {
	return &bongov1.OutboundMessage{
		Id:          id,
		SenderId:    userID,
		SenderEmail: from,
		Recipients:  rcpts,
		BlobHash:    blobHash,
		MessageGuid: messageID,
		Origin:      bongov1.OutboundOrigin_OUTBOUND_ORIGIN_LOCAL,
	}
}

// DeliverGenerated delivers a message the server built for userID, such as a
// calendar invitation, the way a message composed in the web UI is delivered:
// local recipients through the inbound pipeline spool, external ones through
// the outbound queue, which signs for the From domain.
//
// The caller has checked that userID may send as from. sessionEmail keys the
// message rate limiter and userID the recipient budget, the same limiters Send
// spends. No Sent copy and no send log are kept: callers that need either do
// it themselves.
//
// It returns the recipients the message did not reach (quota full, rate
// limited, no spool or queue configured, a write failed) rather than failing
// the batch, so the caller can name them. err describes the first failure for
// logging and is nil when notReached is empty. Duplicate recipients are
// delivered once.
func (h *ComposeHandler) DeliverGenerated(userID, sessionEmail, from string, rcpts []string, raw []byte) (notReached []string, err error) {
	seen := make(map[string]bool, len(rcpts))
	uniq := make([]string, 0, len(rcpts))
	for _, r := range rcpts {
		r = normalizeRecipient(r)
		if r == "" || seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		uniq = append(uniq, r)
	}
	local, external, groups := h.splitRecipients(uniq)
	if len(local)+len(external)+len(groups) == 0 {
		return nil, nil
	}
	fail := func(cause error, addrs ...string) {
		notReached = append(notReached, addrs...)
		if err == nil {
			err = cause
		}
	}

	// Refuse what cannot be delivered before the blob is stored or any budget
	// is spent.
	msgSize := int64(len(raw))
	var spoolable []string
	for _, rcpt := range local {
		switch {
		case h.spool == nil:
			fail(fmt.Errorf("inbound spool not configured"), rcpt)
		case quotaExceeded(h.quota, h.logger, h.resolveLocalUser(rcpt), msgSize):
			fail(fmt.Errorf("recipient mailbox full"), rcpt)
		default:
			spoolable = append(spoolable, rcpt)
		}
	}
	// Groups expand to member copies delivered through the spool. A group that
	// cannot be expanded, would exceed the fan-out cap or has no member who can
	// take the message (none live, or every one over quota) goes in
	// notReached; it is never sent external.
	var members []composeMember
	var groupIDs []string
	// Only direct recipients that will be delivered cover a member: one over
	// quota is not reached, so the group does not count it either.
	reached := h.localUserSet(spoolable)
	for _, g := range groups {
		if h.spool == nil {
			fail(fmt.Errorf("inbound spool not configured"), g.addr)
			continue
		}
		expanded, gerr := h.expandGroups(userID, from, []composeGroup{g})
		if gerr != nil {
			fail(gerr, g.addr)
			continue
		}
		kept, perr := pickMembers(expanded, reached, func(m composeMember) bool {
			if !quotaExceeded(h.quota, h.logger, m.userID, msgSize) {
				return false
			}
			h.logger.Warn("groups: member skipped: over storage quota", "user", m.userID)
			return true
		})
		if perr != nil {
			fail(perr, g.addr)
			continue
		}
		if h.groupFanout != nil && !h.groupFanout.AllowN(groupFanoutKey(userID), len(kept)) {
			fail(fmt.Errorf("group fan-out limit reached"), g.addr)
			continue
		}
		for _, m := range kept {
			reached[m.userID] = true
		}
		members = append(members, kept...)
		groupIDs = append(groupIDs, groupIDsOf(expanded)...)
	}
	var queueable []string
	switch {
	case len(external) == 0:
	case h.queueStore == nil:
		fail(fmt.Errorf("outbound queue not configured"), external...)
	case h.rateLimiter != nil && !h.rateLimiter.Allow(sessionEmail),
		h.recipientRL != nil && !h.recipientRL.AllowN(userID, len(external)):
		fail(fmt.Errorf("rate limit exceeded"), external...)
	default:
		queueable = external
	}
	if len(spoolable)+len(queueable)+len(members) == 0 {
		return notReached, err
	}

	hash, perr := h.blobs.Put(raw)
	if perr != nil {
		fail(fmt.Errorf("storing message: %w", perr), append(spoolable, queueable...)...)
		return notReached, err
	}
	for _, rcpt := range spoolable {
		env := localEnvelope(uuid.NewString(), userID, from, rcpt, hash)
		if serr := h.spool.Enqueue(env); serr != nil {
			h.logger.Warn("web: failed to spool generated message", "user", userID, "error", serr)
			fail(fmt.Errorf("spooling for a local recipient: %w", serr), rcpt)
		}
	}
	if len(members) > 0 {
		memberHash, herr := h.putStamped(raw, groupIDs)
		if herr != nil {
			fail(herr, memberAddrs(members)...)
		} else {
			for _, m := range members {
				env := localEnvelope(uuid.NewString(), userID, from, m.addr, memberHash)
				if serr := h.spool.Enqueue(env); serr != nil {
					h.logger.Warn("groups: failed to spool generated member copy", "user", m.userID, "error", serr)
					fail(fmt.Errorf("spooling for a group member: %w", serr), m.addr)
				}
			}
		}
	}
	if len(queueable) > 0 {
		out := outboundMessage(uuid.NewString(), userID, from, queueable, hash, messageIDOf(raw))
		if qerr := h.queueStore.Enqueue(out); qerr != nil {
			h.logger.Warn("web: failed to enqueue generated message", "user", userID, "error", qerr)
			fail(fmt.Errorf("enqueueing outbound delivery: %w", qerr), queueable...)
		}
	}
	return notReached, err
}

// messageIDOf returns the Message-ID header of raw, or "".
func messageIDOf(raw []byte) string {
	msg, merr := netmail.ReadMessage(bytes.NewReader(raw))
	if merr != nil {
		return ""
	}
	return msg.Header.Get("Message-Id")
}
