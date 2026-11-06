// Package authtest holds test doubles shared by the protocol front ends'
// tests. It is imported only from _test.go files.
package authtest

import (
	"errors"
	"sync/atomic"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"

	"github.com/bongo-project/bongo/internal/auth"
)

// ErrRecordRead is what Records.Get returns while Fail is set.
var ErrRecordRead = errors.New("app password record unreadable")

// Records wraps a real record store and counts Get calls, optionally failing
// them. It lets a test prove that a session opened with the account password
// does no record read, and that a read error fails closed, without failing the
// profile read the way a whole-AccountState fake would.
type Records struct {
	auth.AppPasswordRecords
	// Fail makes Get return ErrRecordRead.
	Fail atomic.Bool
	// Gets counts every Get call.
	Gets atomic.Int32
}

// Get counts the call and fails while Fail is set.
func (r *Records) Get(userID, id string) (*bongov1.AppPassword, bool, error) {
	r.Gets.Add(1)
	if r.Fail.Load() {
		return nil, false, ErrRecordRead
	}
	return r.AppPasswordRecords.Get(userID, id)
}
