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
func (h *ComposeHandler) splitLocal(rcpts []string) (local, external []string) {
	for _, rcpt := range rcpts {
		rcpt = normalizeRecipient(rcpt)
		if rcpt == "" {
			continue
		}
		if h.resolveLocalUser(rcpt) != "" {
			local = append(local, rcpt)
		} else {
			external = append(external, rcpt)
		}
	}
	return local, external
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
	local, external := h.splitLocal(uniq)
	if len(local)+len(external) == 0 {
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
	if len(spoolable)+len(queueable) == 0 {
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
