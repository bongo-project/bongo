package store

import (
	"time"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// placeholderGUID stands in for a Guid the store has not stamped yet. It has
// the same length as generateGUID output so the size estimate is exact.
const placeholderGUID = "00000000000000000000000000000000"

// EventRecordSize returns the number of bytes EventStore.Create will store for
// event: the marshalled protobuf. This is also what the domain usage recompute
// counts for an event, so web quota pre-checks use it to stay consistent.
func EventRecordSize(event *bongov1.Event) int64 {
	c := proto.Clone(event).(*bongov1.Event)
	if c.Guid == "" {
		c.Guid = placeholderGUID
	}
	now := time.Now().Unix()
	c.CreatedAt = now
	c.UpdatedAt = now
	return int64(proto.Size(c))
}

// ContactRecordSize is the contact counterpart of EventRecordSize.
func ContactRecordSize(contact *bongov1.Contact) int64 {
	c := proto.Clone(contact).(*bongov1.Contact)
	if c.Guid == "" {
		c.Guid = placeholderGUID
	}
	now := time.Now().Unix()
	c.CreatedAt = now
	c.UpdatedAt = now
	return int64(proto.Size(c))
}
