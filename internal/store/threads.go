package store

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// ThreadNode represents a single message in a conversation thread.
type ThreadNode struct {
	GUID       string
	MessageID  string
	InReplyTo  string
	Subject    string
	From       string
	ReceivedAt int64
	Flags      int32
	Children   []*ThreadNode
	Depth      int
}

// ThreadSummary represents a complete conversation thread.
type ThreadSummary struct {
	Root     *ThreadNode
	AllNodes []*ThreadNode
	Count    int
}

// ThreadStore builds conversation threads from In-Reply-To chains.
type ThreadStore struct {
	pebble    *PebbleStore
	mailboxes *MailboxStore
}

// NewThreadStore creates a ThreadStore.
func NewThreadStore(pebble *PebbleStore, mailboxes *MailboxStore) *ThreadStore {
	return &ThreadStore{pebble: pebble, mailboxes: mailboxes}
}

// BuildThread constructs a conversation thread starting from the given message GUID.
// It walks up In-Reply-To to find the root, then scans for all children.
func (s *ThreadStore) BuildThread(userID, mailGUID string) (*ThreadSummary, error) {
	// Build a cache of message_id -> MailMeta for all user messages.
	msgIDToMeta := &sync.Map{}
	guidToMeta := make(map[string]*bongov1.MailMeta)

	iter, err := s.pebble.PrefixScan([]byte(fmt.Sprintf("/user/%s/mail/", userID)))
	if err != nil {
		return nil, fmt.Errorf("scanning user mail: %w", err)
	}
	defer iter.Close()

	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		guidToMeta[meta.Guid] = &meta
		if meta.MessageId != "" {
			msgIDToMeta.Store(meta.MessageId, &meta)
		}
	}

	// Find the starting message.
	startMeta, ok := guidToMeta[mailGUID]
	if !ok {
		return nil, fmt.Errorf("message not found: %s", mailGUID)
	}

	// Walk up to find the root.
	root := startMeta
	visited := make(map[string]bool)
	for root.InReplyTo != "" && !visited[root.InReplyTo] {
		visited[root.InReplyTo] = true
		if parent, ok := msgIDToMeta.Load(root.InReplyTo); ok {
			root = parent.(*bongov1.MailMeta)
		} else {
			break
		}
	}

	// Build tree from root.
	// Index: message_id -> children (meta objects whose InReplyTo matches)
	childIndex := make(map[string][]*bongov1.MailMeta)
	for _, meta := range guidToMeta {
		if meta.InReplyTo != "" {
			childIndex[meta.InReplyTo] = append(childIndex[meta.InReplyTo], meta)
		}
	}

	var allNodes []*ThreadNode
	rootNode := buildNode(root, childIndex, 0, &allNodes)

	return &ThreadSummary{
		Root:     rootNode,
		AllNodes: allNodes,
		Count:    len(allNodes),
	}, nil
}

// buildNode recursively builds a ThreadNode tree.
func buildNode(meta *bongov1.MailMeta, childIndex map[string][]*bongov1.MailMeta, depth int, allNodes *[]*ThreadNode) *ThreadNode {
	node := &ThreadNode{
		GUID:       meta.Guid,
		MessageID:  meta.MessageId,
		InReplyTo:  meta.InReplyTo,
		Subject:    meta.Subject,
		From:       meta.From,
		ReceivedAt: meta.ReceivedAt,
		Flags:      meta.Flags,
		Depth:      depth,
	}
	*allNodes = append(*allNodes, node)

	children := childIndex[meta.MessageId]
	sort.Slice(children, func(i, j int) bool {
		return children[i].ReceivedAt < children[j].ReceivedAt
	})

	for _, child := range children {
		childNode := buildNode(child, childIndex, depth+1, allNodes)
		node.Children = append(node.Children, childNode)
	}

	return node
}

// GetThreadRoot returns the root GUID for grouping purposes.
// If the message is a root (no In-Reply-To), returns its own GUID.
func (s *ThreadStore) GetThreadRoot(userID, guid string) (string, error) {
	meta := s.loadMeta(userID, guid)
	if meta == nil {
		return guid, nil
	}
	if meta.InReplyTo == "" {
		return guid, nil
	}

	// Walk up the In-Reply-To chain.
	visited := make(map[string]bool)
	current := meta
	for current.InReplyTo != "" && !visited[current.InReplyTo] {
		visited[current.InReplyTo] = true
		parent := s.findByMessageID(userID, current.InReplyTo)
		if parent == nil {
			break
		}
		current = parent
	}
	return current.Guid, nil
}

// GetThreadCount returns the number of messages in the thread rooted at the given message.
func (s *ThreadStore) GetThreadCount(userID, rootMessageID string) int {
	if rootMessageID == "" {
		return 1
	}

	count := 1 // the root itself
	prefix := fmt.Sprintf("/user/%s/mail/", userID)
	iter, err := s.pebble.PrefixScan([]byte(prefix))
	if err != nil {
		return 1
	}
	defer iter.Close()

	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		// Check if this message references the root message ID anywhere in its chain.
		if meta.InReplyTo == rootMessageID || strings.Contains(meta.References, rootMessageID) {
			count++
		}
	}
	return count
}

func (s *ThreadStore) loadMeta(userID, guid string) *bongov1.MailMeta {
	key := fmt.Sprintf("/user/%s/mail/%s", userID, guid)
	data, err := s.pebble.Get([]byte(key))
	if err != nil {
		return nil
	}
	var meta bongov1.MailMeta
	if err := proto.Unmarshal(data, &meta); err != nil {
		return nil
	}
	return &meta
}

func (s *ThreadStore) findByMessageID(userID, messageID string) *bongov1.MailMeta {
	prefix := fmt.Sprintf("/user/%s/mail/", userID)
	iter, err := s.pebble.PrefixScan([]byte(prefix))
	if err != nil {
		return nil
	}
	defer iter.Close()

	for iter.Next() {
		val, err := iter.Value()
		if err != nil {
			continue
		}
		var meta bongov1.MailMeta
		if err := proto.Unmarshal(val, &meta); err != nil {
			continue
		}
		if meta.MessageId == messageID {
			return &meta
		}
	}
	return nil
}
