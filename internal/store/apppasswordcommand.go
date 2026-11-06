package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

// appPasswordTouchPrefix marks the synthetic last-used command.
const appPasswordTouchPrefix = "/__apppassword_touch__/"

// appPasswordTouchPayload is one touch. The day travels in the payload: the
// FSM must not read the clock.
type appPasswordTouchPayload struct {
	User string `json:"user"`
	ID   string `json:"id"`
	Day  int64  `json:"day"`
}

// DayStart truncates a unix time to 00:00 UTC of its day. Only this
// granularity is ever stored as a last-used time.
func DayStart(unix int64) int64 {
	return unix - ((unix%86400)+86400)%86400
}

// BuildAppPasswordTouchCommand builds the command that moves a credential's
// last_used_at forward to the day containing the given unix time.
//
// A read-modify-write Put of the record from the login path could land after
// a revoke's Delete and bring a revoked credential back with a working hash.
// This command is applied conditionally inside the FSM instead, so it can
// never create a record.
func BuildAppPasswordTouchCommand(userID, id string, at int64) (*bongov1.RaftCommand, error) {
	p := appPasswordTouchPayload{User: userID, ID: id, Day: DayStart(at)}
	if err := validateAppPasswordTouch(&p); err != nil {
		return nil, err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshaling app password touch: %w", err)
	}
	return &bongov1.RaftCommand{
		Op:    bongov1.Op_OP_PUT,
		Key:   []byte(appPasswordTouchPrefix + "touch"),
		Value: data,
	}, nil
}

func validateAppPasswordTouch(p *appPasswordTouchPayload) error {
	if p.User == "" || strings.Contains(p.User, "/") {
		return errors.New("app password touch: invalid user")
	}
	if !ValidAppPasswordID(p.ID) {
		return ErrInvalidAppPasswordID
	}
	if p.Day <= 0 {
		return errors.New("app password touch: invalid day")
	}
	return nil
}

// IsAppPasswordSyntheticCommand reports whether the key is a touch command
// handled directly by the FSM.
func IsAppPasswordSyntheticCommand(key []byte) bool {
	return strings.HasPrefix(string(key), appPasswordTouchPrefix)
}

// PlanAppPasswordTouch computes the write a touch makes: none when the record
// is absent or already at or past the payload day, which also makes log replay
// idempotent.
func PlanAppPasswordTouch(get func(key []byte) ([]byte, error), cmd *bongov1.RaftCommand) (key, value []byte, err error) {
	if !IsAppPasswordSyntheticCommand(cmd.Key) {
		return nil, nil, errors.New("not a synthetic app password command")
	}
	var p appPasswordTouchPayload
	if err := json.Unmarshal(cmd.Value, &p); err != nil {
		return nil, nil, fmt.Errorf("parsing app password touch: %w", err)
	}
	if err := validateAppPasswordTouch(&p); err != nil {
		return nil, nil, err
	}
	k := AppPasswordKey(p.User, p.ID)
	prev, gErr := get(k)
	if gErr != nil && !errors.Is(gErr, ErrNotFound) {
		return nil, nil, fmt.Errorf("reading app password: %w", gErr)
	}
	if len(prev) == 0 {
		return nil, nil, nil
	}
	var rec bongov1.AppPassword
	if err := proto.Unmarshal(prev, &rec); err != nil {
		return nil, nil, fmt.Errorf("unmarshaling app password: %w", err)
	}
	if rec.LastUsedAt >= p.Day {
		return nil, nil, nil
	}
	rec.LastUsedAt = p.Day
	val, err := proto.Marshal(&rec)
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling app password: %w", err)
	}
	return k, val, nil
}

// ApplyAppPasswordTouch applies a touch to ps. The FSM runs it on Raft's
// single goroutine, so the read and the write cannot interleave with a
// revoke's Delete.
func ApplyAppPasswordTouch(ps *PebbleStore, cmd *bongov1.RaftCommand) error {
	k, v, err := PlanAppPasswordTouch(ps.Get, cmd)
	if err != nil || k == nil {
		return err
	}
	return ps.Set(k, v)
}
