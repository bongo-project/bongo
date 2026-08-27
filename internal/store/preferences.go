package store

import (
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// PreferenceStore manages user preferences in Pebble via Raft.
type PreferenceStore struct {
	pebble  *PebbleStore
	applier RaftApplier
}

// NewPreferenceStore creates a PreferenceStore.
func NewPreferenceStore(pebble *PebbleStore, applier RaftApplier) *PreferenceStore {
	return &PreferenceStore{pebble: pebble, applier: applier}
}

// prefKey returns the Pebble key for a user's preferences.
func prefKey(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/preferences", userID))
}

// DefaultPreferences returns sensible defaults for new users.
func DefaultPreferences() *bongov1.UserPreferences {
	return &bongov1.UserPreferences{
		Theme:           bongov1.Theme_THEME_AUTO,
		LayoutMode:      bongov1.LayoutMode_LAYOUT_LIST,
		Density:         bongov1.DisplayDensity_DENSITY_COMFORTABLE,
		MessagesPerPage: 25,
		DefaultReply:    bongov1.DefaultReplyMode_REPLY_MODE_SENDER,
		Timezone:        "UTC",
		Locale:          "en",
		ForwardKeepCopy: true,
	}
}

// Get returns a user's preferences, or defaults if not set.
func (s *PreferenceStore) Get(userID string) *bongov1.UserPreferences {
	data, err := s.pebble.Get(prefKey(userID))
	if err != nil || len(data) == 0 {
		return DefaultPreferences()
	}
	var prefs bongov1.UserPreferences
	if err := proto.Unmarshal(data, &prefs); err != nil {
		return DefaultPreferences()
	}
	return &prefs
}

// Set saves user preferences via Raft.
func (s *PreferenceStore) Set(userID string, prefs *bongov1.UserPreferences) error {
	prefs.UpdatedAt = time.Now().Unix()

	data, err := proto.Marshal(prefs)
	if err != nil {
		return fmt.Errorf("marshaling preferences: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   prefKey(userID),
		Value: data,
	}
	return s.applier.ApplyCommand(cmd, 5*time.Second)
}
