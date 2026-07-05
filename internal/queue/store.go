package queue

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/bongo-go/bongo-go/internal/store"
	bongov1 "github.com/bongo-go/bongo-go/proto/bongo/v1"
)

const (
	pendingPrefix = "/queue/pending/"
	bouncedPrefix = "/queue/bounced/"
)

// QueueStore provides persistent queue operations backed by Pebble + Raft.
type QueueStore struct {
	store   *store.PebbleStore
	applier store.RaftApplier
}

// NewQueueStore creates a new QueueStore.
func NewQueueStore(s *store.PebbleStore, applier store.RaftApplier) *QueueStore {
	return &QueueStore{store: s, applier: applier}
}

// pendingKey builds a key: /queue/pending/{next_retry_at_padded}-{uuid}
func pendingKey(nextRetryAt int64, id string) []byte {
	return []byte(fmt.Sprintf("%s%020d-%s", pendingPrefix, nextRetryAt, id))
}

// bouncedKey builds a key: /queue/bounced/{uuid}
func bouncedKey(id string) []byte {
	return []byte(fmt.Sprintf("%s%s", bouncedPrefix, id))
}

// Enqueue adds a message to the outbound queue via Raft.
func (qs *QueueStore) Enqueue(msg *bongov1.OutboundMessage) error {
	now := time.Now().Unix()
	if msg.NextRetryAt == 0 {
		msg.NextRetryAt = now
	}
	if msg.CreatedAt == 0 {
		msg.CreatedAt = now
	}
	msg.Status = bongov1.MessageStatus_MESSAGE_STATUS_PENDING

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling outbound message: %w", err)
	}

	cmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   pendingKey(msg.NextRetryAt, msg.Id),
		Value: data,
	}
	return qs.applier.ApplyCommand(cmd, 5*time.Second)
}

// GetReady returns messages ready for delivery (next_retry_at <= now).
func (qs *QueueStore) GetReady(now time.Time, batchSize int) ([]*bongov1.OutboundMessage, error) {
	iter, err := qs.store.PrefixScan([]byte(pendingPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning pending queue: %w", err)
	}
	defer iter.Close()

	nowUnix := now.Unix()
	var msgs []*bongov1.OutboundMessage

	for iter.Next() {
		if len(msgs) >= batchSize {
			break
		}

		val, err := iter.Value()
		if err != nil {
			return nil, fmt.Errorf("reading queue value: %w", err)
		}

		var msg bongov1.OutboundMessage
		if err := proto.Unmarshal(val, &msg); err != nil {
			continue
		}

		if msg.NextRetryAt > nowUnix {
			break // keys are sorted by timestamp, so all remaining are in the future
		}

		msgs = append(msgs, &msg)
	}

	return msgs, nil
}

// MarkDelivered removes a message from the pending queue via Raft.
func (qs *QueueStore) MarkDelivered(id string) error {
	key, err := qs.findPendingKey(id)
	if err != nil {
		return err
	}

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: key,
	}
	return qs.applier.ApplyCommand(cmd, 5*time.Second)
}

// MarkFailed increments attempt count and sets exponential backoff via Raft.
func (qs *QueueStore) MarkFailed(msg *bongov1.OutboundMessage) error {
	oldKey, err := qs.findPendingKey(msg.Id)
	if err != nil {
		return err
	}

	// Delete old key.
	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: oldKey,
	}
	if err := qs.applier.ApplyCommand(delCmd, 5*time.Second); err != nil {
		return fmt.Errorf("deleting old queue key: %w", err)
	}

	// Update message.
	msg.AttemptCount++
	msg.Status = bongov1.MessageStatus_MESSAGE_STATUS_FAILED
	msg.NextRetryAt = backoff(msg.AttemptCount)

	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling updated message: %w", err)
	}

	putCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   pendingKey(msg.NextRetryAt, msg.Id),
		Value: data,
	}
	return qs.applier.ApplyCommand(putCmd, 5*time.Second)
}

// MarkBounced moves a message from pending to bounced namespace via Raft.
func (qs *QueueStore) MarkBounced(msg *bongov1.OutboundMessage) error {
	oldKey, err := qs.findPendingKey(msg.Id)
	if err != nil {
		return err
	}

	// Delete from pending.
	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: oldKey,
	}
	if err := qs.applier.ApplyCommand(delCmd, 5*time.Second); err != nil {
		return fmt.Errorf("deleting pending key: %w", err)
	}

	// Write to bounced namespace.
	msg.Status = bongov1.MessageStatus_MESSAGE_STATUS_BOUNCED
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling bounced message: %w", err)
	}

	putCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   bouncedKey(msg.Id),
		Value: data,
	}
	return qs.applier.ApplyCommand(putCmd, 5*time.Second)
}

// Stats returns queue statistics.
func (qs *QueueStore) Stats() (*bongov1.QueueStats, error) {
	stats := &bongov1.QueueStats{}
	now := time.Now().Unix()

	// Count pending.
	iter, err := qs.store.PrefixScan([]byte(pendingPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning pending: %w", err)
	}
	oldestSet := false
	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			closeErr := iter.Close()
			return nil, fmt.Errorf("reading pending value: %w", errors.Join(err, closeErr))
		}
		var msg bongov1.OutboundMessage
		if err := proto.Unmarshal(val, &msg); err != nil {
			continue
		}
		stats.PendingCount++
		if !oldestSet {
			stats.OldestPendingSeconds = now - msg.CreatedAt
			oldestSet = true
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("closing pending iterator: %w", err)
	}

	// Count bounced.
	biter, err := qs.store.PrefixScan([]byte(bouncedPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning bounced: %w", err)
	}
	for biter.Next() {
		stats.FailedCount++
	}
	if err := biter.Close(); err != nil {
		return nil, fmt.Errorf("closing bounced iterator: %w", err)
	}

	return stats, nil
}

// Get retrieves a message by ID from the pending queue.
func (qs *QueueStore) Get(id string) (*bongov1.OutboundMessage, error) {
	key, err := qs.findPendingKey(id)
	if err != nil {
		return nil, err
	}

	data, err := qs.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("reading queue message: %w", err)
	}

	var msg bongov1.OutboundMessage
	if err := proto.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("parsing queue message: %w", err)
	}
	return &msg, nil
}

// Delete removes a message from the pending queue via Raft.
func (qs *QueueStore) Delete(id string) error {
	key, err := qs.findPendingKey(id)
	if err != nil {
		return err
	}

	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: key,
	}
	return qs.applier.ApplyCommand(cmd, 5*time.Second)
}

// RetryNow resets next_retry_at to now for immediate retry via Raft.
func (qs *QueueStore) RetryNow(id string) error {
	msg, err := qs.Get(id)
	if err != nil {
		return err
	}

	oldKey, err := qs.findPendingKey(id)
	if err != nil {
		return err
	}

	// Delete old key.
	delCmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: oldKey,
	}
	if err := qs.applier.ApplyCommand(delCmd, 5*time.Second); err != nil {
		return fmt.Errorf("deleting old key: %w", err)
	}

	// Write with current timestamp.
	msg.NextRetryAt = time.Now().Unix()
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}

	putCmd := &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   pendingKey(msg.NextRetryAt, msg.Id),
		Value: data,
	}
	return qs.applier.ApplyCommand(putCmd, 5*time.Second)
}

// ListPending returns up to limit pending messages.
func (qs *QueueStore) ListPending(limit int) ([]*bongov1.OutboundMessage, error) {
	iter, err := qs.store.PrefixScan([]byte(pendingPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning pending: %w", err)
	}
	defer iter.Close()

	var msgs []*bongov1.OutboundMessage
	for iter.Next() {
		if len(msgs) >= limit {
			break
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var msg bongov1.OutboundMessage
		if err := proto.Unmarshal(val, &msg); err != nil {
			continue
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

// ListBounced returns up to limit bounced messages.
func (qs *QueueStore) ListBounced(limit int) ([]*bongov1.OutboundMessage, error) {
	iter, err := qs.store.PrefixScan([]byte(bouncedPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning bounced: %w", err)
	}
	defer iter.Close()

	var msgs []*bongov1.OutboundMessage
	for iter.Next() {
		if len(msgs) >= limit {
			break
		}
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var msg bongov1.OutboundMessage
		if err := proto.Unmarshal(val, &msg); err != nil {
			continue
		}
		msgs = append(msgs, &msg)
	}
	return msgs, nil
}

// FlushQueue resets next_retry_at to now for all pending messages.
func (qs *QueueStore) FlushQueue() (int, error) {
	pending, err := qs.ListPending(1000)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, msg := range pending {
		if err := qs.RetryNow(msg.Id); err == nil {
			count++
		}
	}
	return count, nil
}

// DeleteBounced removes a bounced message by ID.
func (qs *QueueStore) DeleteBounced(id string) error {
	cmd := &bongov1.RaftCommand{
		Op:  bongov1.Op_OP_DELETE,
		Key: bouncedKey(id),
	}
	return qs.applier.ApplyCommand(cmd, 5*time.Second)
}

// findPendingKey scans the pending prefix to find the full key for a message ID.
func (qs *QueueStore) findPendingKey(id string) ([]byte, error) {
	iter, err := qs.store.PrefixScan([]byte(pendingPrefix))
	if err != nil {
		return nil, fmt.Errorf("scanning for message %s: %w", id, err)
	}
	defer iter.Close()

	for iter.Next() {
		key := string(iter.Key())
		if strings.HasSuffix(key, "-"+id) {
			return []byte(key), nil
		}
	}
	return nil, fmt.Errorf("message %s not found in queue", id)
}

// backoff computes the next retry timestamp using exponential backoff.
// attempt 1 = 2min, attempt 5 = ~32min, capped at 24h.
func backoff(attempt int32) int64 {
	const maxSeconds int64 = 24 * 60 * 60 // 24 hours
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 10 {
		return time.Now().Unix() + maxSeconds
	}
	seconds := (int64(1) << attempt) * 60 // 2^attempt * 60 seconds
	return time.Now().Unix() + seconds
}
