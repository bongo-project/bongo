package pipeline

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// SpamThresholds is the antispam tag/quarantine pair. Tag is the score at or
// above which a message is tagged as spam and filed in Junk (and stamped
// X-Spam-Status: Yes); High is the score at or above which it is quarantined.
// The two are always read and published together.
type SpamThresholds struct {
	Tag  float32
	High float32
}

// Validate applies the same rules as config.AntispamBuiltin's accessors:
// each value in (0, 1] and High >= Tag. Equality is allowed because config
// clamps High up to Tag. The checks are written so NaN is rejected.
func (t SpamThresholds) Validate() error {
	if !(t.Tag > 0 && t.Tag <= 1) {
		return fmt.Errorf("spam tag threshold %v out of range (0, 1]", t.Tag)
	}
	if !(t.High > 0 && t.High <= 1) {
		return fmt.Errorf("spam high threshold %v out of range (0, 1]", t.High)
	}
	if t.High < t.Tag {
		return fmt.Errorf("spam high threshold %v is below tag threshold %v", t.High, t.Tag)
	}
	return nil
}

// SpamThresholdHolder publishes a SpamThresholds pair atomically. One holder
// per process feeds the builtin antispam processor and the deliver processor,
// so a reader never sees a new Tag with an old High.
type SpamThresholdHolder struct {
	p atomic.Pointer[SpamThresholds]
}

// NewSpamThresholdHolder returns a holder initialised with t, or an error if
// t is invalid.
func NewSpamThresholdHolder(t SpamThresholds) (*SpamThresholdHolder, error) {
	h := &SpamThresholdHolder{}
	if err := h.Store(t); err != nil {
		return nil, err
	}
	return h, nil
}

// Store validates t and publishes it. On error the held pair is unchanged.
func (h *SpamThresholdHolder) Store(t SpamThresholds) error {
	if h == nil {
		return errors.New("nil spam threshold holder")
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("store spam thresholds: %w", err)
	}
	cp := t
	h.p.Store(&cp)
	return nil
}

// Thresholds returns the current pair. Callers should load once per decision.
func (h *SpamThresholdHolder) Thresholds() SpamThresholds {
	return *h.p.Load()
}
