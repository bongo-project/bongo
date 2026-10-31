package rules

import (
	"fmt"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/store"
)

// RaftApplier applies commands through Raft consensus.
type RaftApplier interface {
	ApplyCommand(cmd *bongov1.RaftCommand, timeout time.Duration) error
}

// RuleStore manages per-user filter rules in Pebble via Raft.
type RuleStore struct {
	pebble  *store.PebbleStore
	applier RaftApplier
}

// NewRuleStore creates a RuleStore.
func NewRuleStore(pebble *store.PebbleStore, applier RaftApplier) *RuleStore {
	return &RuleStore{pebble: pebble, applier: applier}
}

func ruleKey(userID, ruleID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/rule/%s", userID, ruleID))
}

func rulePrefix(userID string) []byte {
	return []byte(fmt.Sprintf("/user/%s/rule/", userID))
}

// List returns all filter rules for a user, sorted by priority.
func (s *RuleStore) List(userID string) ([]*bongov1.FilterRule, error) {
	iter, err := s.pebble.PrefixScan(rulePrefix(userID))
	if err != nil {
		return nil, fmt.Errorf("scanning rules: %w", err)
	}
	defer iter.Close()

	var rules []*bongov1.FilterRule
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var rule bongov1.FilterRule
		if err := proto.Unmarshal(val, &rule); err != nil {
			continue
		}
		rules = append(rules, &rule)
	}

	// Order now decides which rule's actions win, so equal priorities must not
	// resolve differently from one read to the next.
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return a.Id < b.Id
	})

	return rules, nil
}

// Get returns a single filter rule.
func (s *RuleStore) Get(userID, ruleID string) (*bongov1.FilterRule, error) {
	data, err := s.pebble.Get(ruleKey(userID, ruleID))
	if err != nil {
		return nil, fmt.Errorf("rule not found: %w", err)
	}

	var rule bongov1.FilterRule
	if err := proto.Unmarshal(data, &rule); err != nil {
		return nil, fmt.Errorf("parsing rule: %w", err)
	}
	return &rule, nil
}

// Save creates or updates a filter rule via Raft.
func (s *RuleStore) Save(userID string, rule *bongov1.FilterRule) error {
	data, err := proto.Marshal(rule)
	if err != nil {
		return fmt.Errorf("marshaling rule: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   ruleKey(userID, rule.Id),
		Value: data,
	}
	return s.applier.ApplyCommand(cmd, 5*time.Second)
}

// Delete removes a filter rule via Raft.
func (s *RuleStore) Delete(userID, ruleID string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: ruleKey(userID, ruleID),
	}
	return s.applier.ApplyCommand(cmd, 5*time.Second)
}

// Reorder updates the priority field on each rule to match the given order.
func (s *RuleStore) Reorder(userID string, ruleIDs []string) error {
	for i, id := range ruleIDs {
		rule, err := s.Get(userID, id)
		if err != nil {
			return fmt.Errorf("loading rule %s: %w", id, err)
		}
		rule.Priority = int32(i + 1)
		rule.UpdatedAt = time.Now().Unix()
		if err := s.Save(userID, rule); err != nil {
			return fmt.Errorf("saving rule %s: %w", id, err)
		}
	}
	return nil
}
