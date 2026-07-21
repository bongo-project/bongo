package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestAttachmentImagesHiddenUntilRevealed verifies image attachments are not
// auto-loaded: the <img> lives inside an x-if template that only instantiates
// once the user clicks "Show images", and a reveal control is rendered.
func TestAttachmentImagesHiddenUntilRevealed(t *testing.T) {
	atts := []Attachment{
		{Filename: "pic.png", ContentType: "image/png", Size: 10, URL: "/mail/INBOX/1/attachment/0"},
	}

	var buf bytes.Buffer
	if err := attachmentList(atts).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render attachmentList: %v", err)
	}
	html := buf.String()

	if !strings.Contains(html, `x-data="{ showImages: false }"`) {
		t.Fatal("attachment list missing the showImages reveal state")
	}
	if !strings.Contains(html, `class="attachment-reveal"`) {
		t.Fatal("missing the 'Show images' reveal control")
	}
	// The thumbnail must be gated behind x-if so it is not in the DOM (and never
	// fetched) until the user opts in.
	tmplIdx := strings.Index(html, `<template x-if="showImages">`)
	imgIdx := strings.Index(html, `class="attachment-thumbnail"`)
	if tmplIdx < 0 || imgIdx < 0 || tmplIdx > imgIdx {
		t.Fatal("image thumbnail is not gated behind <template x-if=\"showImages\">")
	}
	// A hidden placeholder is shown in the meantime.
	if !strings.Contains(html, "attachment-chip-hidden") {
		t.Error("missing the hidden-image placeholder chip")
	}
}

// TestNonImageAttachmentNotGated: a zip has no reveal control and downloads directly.
func TestNonImageAttachmentNotGated(t *testing.T) {
	atts := []Attachment{
		{Filename: "a.zip", ContentType: "application/zip", Size: 20, URL: "/mail/INBOX/1/attachment/0"},
	}
	var buf bytes.Buffer
	if err := attachmentList(atts).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()
	if strings.Contains(html, `class="attachment-reveal"`) {
		t.Error("non-image attachments should not show the image reveal control")
	}
	if !strings.Contains(html, `href="/mail/INBOX/1/attachment/0"`) {
		t.Error("zip attachment should link straight to its download URL")
	}
}
