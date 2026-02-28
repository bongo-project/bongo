package store

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

// tagColourPaletteSize is the number of colours in the cycling palette.
const tagColourPaletteSize = 16

// TagStore manages per-user tag definitions in Pebble via Raft.
type TagStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewTagStore creates a new TagStore.
func NewTagStore(pebble *PebbleStore, applier RaftApplier) *TagStore {
	return &TagStore{pebble: pebble, applier: applier}
}

// TagWithCount combines a TagMeta with the number of messages bearing that tag.
type TagWithCount struct {
	Tag   *bongov1.TagMeta
	Count int
}

// tagKey returns the Pebble key for a tag definition.
func tagKey(userID, name string) []byte {
	return []byte(fmt.Sprintf("/user/%s/tag/%s", userID, name))
}

// tagPrefix returns the Pebble key prefix for all tags belonging to a user.
func tagPrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/tag/", userID))
}

// GetTag returns the TagMeta for a single tag name.
// Returns ErrNotFound if the tag does not exist.
func (ts *TagStore) GetTag(userID, name string) (*bongov1.TagMeta, error) {
	data, err := ts.pebble.Get(tagKey(userID, name))
	if err != nil {
		return nil, fmt.Errorf("tag %q: %w", name, err)
	}
	var meta bongov1.TagMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parsing tag %q: %w", name, err)
	}
	return &meta, nil
}

// ListTags returns all tags for a user, sorted by sort_order ascending then
// name ascending. Each entry includes the count of messages bearing that tag.
func (ts *TagStore) ListTags(userID string) ([]TagWithCount, error) {
	prefix := tagPrefix(userID)
	iter, err := ts.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("listing tags: %w", err)
	}
	defer iter.Close()

	var tags []*bongov1.TagMeta
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading tag value: %w", err)
		}
		var meta bongov1.TagMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			// Skip malformed records.
			continue
		}
		tags = append(tags, &meta)
	}

	// Sort by sort_order ascending, then name ascending.
	slices.SortFunc(tags, func(a, b *bongov1.TagMeta) int {
		if n := cmp.Compare(a.SortOrder, b.SortOrder); n != 0 {
			return n
		}
		return cmp.Compare(a.Name, b.Name)
	})

	// Build per-tag message counts from mail meta prefix scan.
	counts, err := ts.countTagsFromMailMeta(userID, tags)
	if err != nil {
		// Non-fatal: return tags without counts rather than failing entirely.
		counts = make(map[string]int)
	}

	result := make([]TagWithCount, len(tags))
	for i, t := range tags {
		result[i] = TagWithCount{Tag: t, Count: counts[t.Name]}
	}
	return result, nil
}

// countTagsFromMailMeta scans all MailMeta records for a user and returns a
// map of tag name -> message count. Only tags present in the provided list are
// counted; this avoids allocating an unbounded map for large mailboxes.
func (ts *TagStore) countTagsFromMailMeta(userID string, tags []*bongov1.TagMeta) (map[string]int, error) {
	// Build a set of known tag names so we only count tags that exist.
	known := make(map[string]bool, len(tags))
	for _, t := range tags {
		known[t.Name] = true
	}

	prefix := mailMetaPrefix(userID)
	iter, err := ts.pebble.PrefixScan(prefix)
	if err != nil {
		return nil, fmt.Errorf("scanning mail meta for tag counts: %w", err)
	}
	defer iter.Close()

	counts := make(map[string]int, len(tags))
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading mail meta value: %w", err)
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		for _, t := range meta.Tags {
			if known[t] {
				counts[t]++
			}
		}
	}
	return counts, nil
}

// CreateTag creates a new tag for a user.
// The colour is auto-assigned by cycling through the palette based on the
// current tag count. Returns an error if the tag already exists, the name is
// invalid, or the per-user limit would be exceeded.
func (ts *TagStore) CreateTag(userID, name string) (*bongov1.TagMeta, error) {
	if err := ValidateTagName(name); err != nil {
		return nil, err
	}

	// Check for duplicate.
	if _, err := ts.pebble.Get(tagKey(userID, name)); err == nil {
		return nil, fmt.Errorf("tag %q already exists", name)
	}

	// Count existing tags to enforce limit and assign sort_order / colour.
	existing, err := ts.countTags(userID)
	if err != nil {
		return nil, fmt.Errorf("counting tags: %w", err)
	}
	if existing >= MaxTagsPerUser {
		return nil, fmt.Errorf("user has reached the maximum of %d tags", MaxTagsPerUser)
	}

	meta := &bongov1.TagMeta{
		Name:        name,
		ColourIndex: int32(existing % tagColourPaletteSize),
		CreatedAt:   time.Now().Unix(),
		SortOrder:   int32(existing),
	}

	data, err := proto.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling tag: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   tagKey(userID, name),
		Value: data,
	}
	if err := ts.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return nil, fmt.Errorf("applying create tag command: %w", err)
	}
	return meta, nil
}

// RenameTag renames an existing tag.
// It reads the old TagMeta, writes it under the new key, then deletes the old
// key. Both operations go through Raft.
func (ts *TagStore) RenameTag(userID, oldName, newName string) (*bongov1.TagMeta, error) {
	if err := ValidateTagName(newName); err != nil {
		return nil, err
	}
	if oldName == newName {
		return nil, fmt.Errorf("new tag name is the same as the old name")
	}

	// Ensure old tag exists.
	old, err := ts.GetTag(userID, oldName)
	if err != nil {
		return nil, fmt.Errorf("reading tag %q: %w", oldName, err)
	}

	// Ensure new name is not already taken.
	if _, err := ts.pebble.Get(tagKey(userID, newName)); err == nil {
		return nil, fmt.Errorf("tag %q already exists", newName)
	}

	// Build new TagMeta preserving all fields except the name.
	newMeta := &bongov1.TagMeta{
		Name:        newName,
		ColourIndex: old.ColourIndex,
		CreatedAt:   old.CreatedAt,
		SortOrder:   old.SortOrder,
	}
	data, err := proto.Marshal(newMeta)
	if err != nil {
		return nil, fmt.Errorf("marshaling renamed tag: %w", err)
	}

	// Write new key.
	putCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   tagKey(userID, newName),
		Value: data,
	}
	if err := ts.applier.ApplyCommand(putCmd, 5*time.Second); err != nil {
		return nil, fmt.Errorf("writing renamed tag: %w", err)
	}

	// Delete old key.
	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: tagKey(userID, oldName),
	}
	if err := ts.applier.ApplyCommand(delCmd, 5*time.Second); err != nil {
		return nil, fmt.Errorf("deleting old tag key: %w", err)
	}

	return newMeta, nil
}

// DeleteTag removes a tag definition from Pebble via Raft.
// Returns ErrNotFound if the tag does not exist.
func (ts *TagStore) DeleteTag(userID, name string) error {
	// Confirm existence before issuing delete.
	if _, err := ts.pebble.Get(tagKey(userID, name)); err != nil {
		return fmt.Errorf("tag %q: %w", name, err)
	}

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: tagKey(userID, name),
	}
	if err := ts.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("applying delete tag command: %w", err)
	}
	return nil
}

// UpdateTagColour changes the colour_index of an existing tag via Raft.
// colourIndex must be in [0, tagColourPaletteSize).
func (ts *TagStore) UpdateTagColour(userID, name string, colourIndex int32) (*bongov1.TagMeta, error) {
	if colourIndex < 0 || colourIndex >= tagColourPaletteSize {
		return nil, fmt.Errorf("colour index %d out of range [0, %d)", colourIndex, tagColourPaletteSize)
	}

	meta, err := ts.GetTag(userID, name)
	if err != nil {
		return nil, fmt.Errorf("reading tag %q for colour update: %w", name, err)
	}

	meta.ColourIndex = colourIndex

	data, err := proto.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("marshaling tag: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   tagKey(userID, name),
		Value: data,
	}
	if err := ts.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return nil, fmt.Errorf("applying update colour command: %w", err)
	}
	return meta, nil
}

// countTags returns the number of tag definitions stored for a user.
func (ts *TagStore) countTags(userID string) (int, error) {
	prefix := tagPrefix(userID)
	iter, err := ts.pebble.PrefixScan(prefix)
	if err != nil {
		return 0, fmt.Errorf("counting tags: %w", err)
	}
	defer iter.Close()

	var n int
	for iter.Next() {
		n++
	}
	return n, nil
}
