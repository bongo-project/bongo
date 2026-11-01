package rules

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// firstMatchMigrationKey marks the one-off first-match migration as complete.
var firstMatchMigrationKey = []byte("/system/migrations/rules-first-match-v1")

// FirstMatchMigrationDone reports whether MigrateFirstMatch has completed.
func (s *RuleStore) FirstMatchMigrationDone() (bool, error) {
	_, err := s.pebble.Get(firstMatchMigrationKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("rules first-match migration marker: %w", err)
	}
	return true, nil
}

// MarkFirstMatchMigrationDone records, via Raft, that the migration completed.
func (s *RuleStore) MarkFirstMatchMigrationDone() error {
	cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: firstMatchMigrationKey, Value: []byte("done")}
	if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
		return fmt.Errorf("rules first-match migration marker: raft: %w", err)
	}
	return nil
}

// MigrateFirstMatch keeps rules written before several-actions-per-rule
// behaving as they did: only the first matching rule fired. Every such rule
// gets stop_processing = true and its single action copied into the actions
// list.
//
// A pre-release rule is recognised exactly by an empty actions list (the form
// always writes both fields), so a rule saved by the new form is never
// touched and a rerun after a crash skips what an earlier run already wrote,
// whether or not the marker exists. Each rule is one PUT; the marker is the
// caller's last write, so a crash part-way is resumed by the next run. The
// run may overlap user edits when it starts after boot, so each rule is
// re-read just before its PUT; the remaining window is a read-then-PUT with no
// compare-and-set, which the release deliberately does not add. The
// writes are not atomic as a set: until the run finishes, some rules may fire
// first-match and others not. It returns the number of rules rewritten.
func (s *RuleStore) MigrateFirstMatch() (int, error) {
	iter, err := s.pebble.PrefixScan([]byte("/user/"))
	if err != nil {
		return 0, fmt.Errorf("scanning rules for first-match migration: %w", err)
	}
	type pending struct {
		key  []byte
		rule *bongov1.FilterRule
	}
	var todo []pending
	for iter.Next() {
		parts := strings.Split(string(iter.Key()), "/")
		if len(parts) != 5 || parts[3] != "rule" {
			continue
		}
		val, vErr := iter.Value()
		if vErr != nil {
			continue
		}
		var rule bongov1.FilterRule
		if proto.Unmarshal(val, &rule) != nil || len(rule.Actions) > 0 {
			continue
		}
		todo = append(todo, pending{key: append([]byte(nil), iter.Key()...), rule: &rule})
	}
	if err := iter.Close(); err != nil {
		return 0, fmt.Errorf("closing rule scan: %w", err)
	}

	written := 0
	for _, p := range todo {
		p.rule.StopProcessing = true
		if p.rule.Action != nil {
			SetActions(p.rule, []*bongov1.FilterAction{p.rule.Action})
		}
		data, err := proto.Marshal(p.rule)
		if err != nil {
			return written, fmt.Errorf("marshaling rule %s: %w", p.rule.Id, err)
		}
		// The scan above may be stale when the run starts after the server is
		// already serving: skip a rule a user has since deleted, or saved in
		// the new form. A small window between this read and the PUT remains.
		cur, gErr := s.pebble.Get(p.key)
		if errors.Is(gErr, store.ErrNotFound) {
			continue
		}
		if gErr == nil {
			var now bongov1.FilterRule
			if proto.Unmarshal(cur, &now) == nil && len(now.Actions) > 0 {
				continue
			}
		}
		cmd := &bongov1.RaftCommand{Op: bongov1.Op_OP_PUT, Key: p.key, Value: data}
		if err := s.applier.ApplyCommand(cmd, 5*time.Second); err != nil {
			return written, fmt.Errorf("migrating rule %s: %w", p.rule.Id, err)
		}
		written++
	}
	return written, nil
}
