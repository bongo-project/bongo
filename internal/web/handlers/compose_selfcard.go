package handlers

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/emersion/go-vcard"

	"github.com/bongo-project/bongo/internal/contactio"
	"github.com/bongo-project/bongo/internal/web/templates"
	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// selfCardMaxBytes caps the encoded card. Without PHOTO a card is a few hundred
// bytes, so this only guards a pathological record.
const selfCardMaxBytes = 64 << 10

// selfCardContentType is the part type of the attached card.
const selfCardContentType = "text/vcard; charset=utf-8"

var (
	errNoSelfCard       = errors.New("no own contact card is marked")
	errSelfCardTooLarge = errors.New("own contact card too large to attach")
)

// selfCardAttachment is the user's own card as a .vcf attachment: the card
// marked "This is me", cut down to the shared allowlist (see contactio.ShareCard).
// It is the session user's own keyspace only. errNoSelfCard means nothing
// resolves (no mark, a dangling mark, or a contact group).
func (h *ComposeHandler) selfCardAttachment(userID string) (attachmentData, *bongov1.BlobRef, error) {
	if h.contacts == nil {
		return attachmentData{}, nil, errNoSelfCard
	}
	c, ok := h.contacts.SelfContact(userID)
	if !ok {
		return attachmentData{}, nil, errNoSelfCard
	}
	card := contactio.ShareCard(contactCard(c))
	var buf bytes.Buffer
	if err := vcard.NewEncoder(&buf).Encode(card); err != nil {
		return attachmentData{}, nil, fmt.Errorf("encoding own card: %w", err)
	}
	if buf.Len() > selfCardMaxBytes {
		return attachmentData{}, nil, errSelfCardTooLarge
	}
	name := contactio.CardFilename(card.PreferredValue(vcard.FieldFormattedName))
	data := buf.Bytes()
	return attachmentData{Filename: name, ContentType: selfCardContentType, Data: data},
		&bongov1.BlobRef{Size: int64(len(data)), ContentType: selfCardContentType, Filename: name}, nil
}

// selfCardOption is what the compose form needs for "Attach my card": the box
// is offered only when the user has a card marked.
func (h *ComposeHandler) selfCardOption(userID string, checked bool) templates.SelfCardOption {
	if h.contacts == nil {
		return templates.SelfCardOption{}
	}
	c, ok := h.contacts.SelfContact(userID)
	if !ok {
		return templates.SelfCardOption{}
	}
	name := c.DisplayName
	if name == "" {
		name = c.Email
	}
	if name == "" {
		name = "-"
	}
	return templates.SelfCardOption{Name: name, Checked: checked}
}

// attachSelfHeader marks a draft that carries the user's card, so reopening it
// ticks the box again. It is for the stored draft only; Send never adds it.
const attachSelfHeader = "X-Bongo-Attach-Self"
