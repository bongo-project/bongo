package store

import (
	"fmt"
	"regexp"
	"slices"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	// MaxTagsPerMessage is the maximum number of tags allowed on a single message.
	MaxTagsPerMessage = 20
	// MaxTagsPerUser is the maximum number of distinct tag names across all messages for a user.
	MaxTagsPerUser = 100
	// MaxTagNameLen is the maximum byte length of a tag name.
	MaxTagNameLen = 50
)

// tagNameRe matches valid tag names: lowercase alphanumeric and hyphens, non-empty,
// must start and end with an alphanumeric character.
var tagNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidateTagName returns an error if name is not a valid tag name.
// Valid names are lowercase, alphanumeric + hyphens only, 1-50 characters,
// must start and end with a letter or digit (not a hyphen).
func ValidateTagName(name string) error {
	if name == "" {
		return fmt.Errorf("tag name must not be empty")
	}
	if len(name) > MaxTagNameLen {
		return fmt.Errorf("tag name %q exceeds maximum length of %d characters", name, MaxTagNameLen)
	}
	if !tagNameRe.MatchString(name) {
		return fmt.Errorf("tag name %q is invalid: must be lowercase alphanumeric and hyphens only, starting and ending with alphanumeric", name)
	}
	return nil
}

// mailMetaKey returns the Pebble key for a MailMeta record.
func mailMetaKey(userID, mailGUID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/mail/%s", userID, mailGUID))
}

// mailMetaPrefix returns the Pebble key prefix for all MailMeta records for a user.
func mailMetaPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/mail/", userID))
}

// GetTags reads a message's MailMeta from Pebble and returns its tags.
// Returns ErrNotFound if the message does not exist.
func (ms *MailboxStore) GetTags(userID, mailGUID string) ([]string, error) {
	data, err := ms.store.Get(mailMetaKey(userID, mailGUID))
	if err != nil {
		return nil, fmt.Errorf("getting mail meta for tags: %w", err)
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing mail meta: %w", err)
	}
	return slices.Clone(meta.Tags), nil
}

// SetTags replaces all tags on meta with the provided list.
// It validates each tag name and enforces the per-message limit.
// This is a pure in-memory operation; the caller must persist meta via Raft.
func SetTags(meta *bongov1.MailMeta, tags []string) error {
	deduped := deduplicateTags(tags)
	for _, name := range deduped {
		if err := ValidateTagName(name); err != nil {
			return err
		}
	}
	if len(deduped) > MaxTagsPerMessage {
		return fmt.Errorf("message would have %d tags, exceeding the limit of %d", len(deduped), MaxTagsPerMessage)
	}
	meta.Tags = deduped
	return nil
}

// AddTags adds tags to meta, merging with existing tags.
// It validates each new tag name, deduplicates, and enforces the per-message
// limit. The caller is responsible for enforcing the per-user distinct tag limit
// before calling this function (use CountUserDistinctTags).
// This is a pure in-memory operation; the caller must persist meta via Raft.
func AddTags(meta *bongov1.MailMeta, tags []string) error {
	for _, name := range tags {
		if err := ValidateTagName(name); err != nil {
			return err
		}
	}
	merged := deduplicateTags(append(slices.Clone(meta.Tags), tags...))
	if len(merged) > MaxTagsPerMessage {
		return fmt.Errorf("message would have %d tags, exceeding the per-message limit of %d", len(merged), MaxTagsPerMessage)
	}
	meta.Tags = merged
	return nil
}

// RemoveTags removes the specified tags from meta.
// Tags not present on the message are silently ignored.
// This is a pure in-memory operation; the caller must persist meta via Raft.
func RemoveTags(meta *bongov1.MailMeta, tags []string) error {
	remove := make(map[string]bool, len(tags))
	for _, t := range tags {
		remove[t] = true
	}
	kept := meta.Tags[:0:0] // empty slice, avoids aliasing the backing array
	for _, t := range meta.Tags {
		if !remove[t] {
			kept = append(kept, t)
		}
	}
	meta.Tags = kept
	return nil
}

// CountUserDistinctTags scans all MailMeta records for a user and returns
// the number of distinct tag names used across all messages.
// This is used to enforce the per-user tag limit before creating new tags.
func (ms *MailboxStore) CountUserDistinctTags(userID string) (int, error) {
	prefix := mailMetaPrefix(userID)
	iter, err := ms.store.PrefixScan(prefix)
	if err != nil {
		return 0, fmt.Errorf("scanning mail meta for tag count: %w", err)
	}
	defer iter.Close()

	seen := make(map[string]bool)
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return 0, fmt.Errorf("reading mail meta value: %w", err)
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			// Skip malformed records; don't fail the whole count.
			continue
		}
		for _, t := range meta.Tags {
			seen[t] = true
		}
	}
	return len(seen), nil
}

// deduplicateTags returns a new slice with duplicate tag names removed,
// preserving order of first occurrence.
func deduplicateTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
