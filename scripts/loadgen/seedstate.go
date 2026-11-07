package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// The state file is append-only JSON lines, synced after each record. It is
// the only place a generated password is written, so it must stay mode 0600,
// outside the repository, and out of every report.

const (
	recHeader  = "header"
	recPending = "user-pending"
	recUser    = "user-done"
	recSending = "msg-sending"
	recMsg     = "msg-done"
)

// stateHeader binds a state file to one plan. Every field that changes the
// plan is here, so a resume under different parameters is refused rather
// than mixing two plans.
type stateHeader struct {
	Seed       uint64 `json:"seed"`
	Users      int    `json:"users"`
	MPU        int    `json:"messages_per_user"`
	Domain     string `json:"domain"`
	SizeMix    string `json:"size_mix"`
	FolderMix  string `json:"folder_mix"`
	DedupShare string `json:"dedup_share"`
	DedupMin   int    `json:"dedup_min_size"`
	Pool       int    `json:"attachment_pool"`
}

func (s PlanSpec) header() stateHeader {
	return stateHeader{
		Seed: s.Seed, Users: s.Users, MPU: s.MPU, Domain: s.Domain,
		SizeMix: canonMix(s.Sizes), FolderMix: canonMix(s.Folders),
		DedupShare: strconv.FormatFloat(s.DedupShare, 'g', -1, 64),
		DedupMin:   s.DedupMin, Pool: s.PoolSize,
	}
}

// diff names the first field that differs, or "" when equal.
func (h stateHeader) diff(o stateHeader) string {
	switch {
	case h.Seed != o.Seed:
		return "seed"
	case h.Users != o.Users:
		return "users"
	case h.MPU != o.MPU:
		return "messages-per-user"
	case h.Domain != o.Domain:
		return "test-domain"
	case h.SizeMix != o.SizeMix:
		return "size-mix"
	case h.FolderMix != o.FolderMix:
		return "folder-mix"
	case h.DedupShare != o.DedupShare:
		return "dedup-share"
	case h.DedupMin != o.DedupMin:
		return "dedup-min-size"
	case h.Pool != o.Pool:
		return "attachment-pool"
	}
	return ""
}

// record is one line. Pw is a plain string holding Password.Reveal():
// marshalling a Password would write "[redacted]" and silently lose the
// credential.
type record struct {
	T      string       `json:"t"`
	U      int          `json:"u,omitempty"`
	M      int          `json:"m,omitempty"`
	Email  string       `json:"email,omitempty"`
	Pw     string       `json:"pw,omitempty"`
	Header *stateHeader `json:"header,omitempty"`
}

type msgKey struct{ u, m int }

type userState struct {
	email   string
	pw      string
	pending bool
	done    bool
}

type msgStatus int

const (
	msgUnknown msgStatus = iota
	msgSending
	msgDone
)

type seedState struct {
	mu    sync.Mutex
	f     *os.File
	users map[int]*userState
	msgs  map[msgKey]msgStatus
}

// defaultStatePath keeps the file under the user's config directory, outside
// any checkout. The name hashes the run identity so two deployments do not
// share one file by accident.
func defaultStatePath(identity ...string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find config dir (pass --state-file): %w", err)
	}
	h := sha256.New()
	for _, s := range identity {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return filepath.Join(dir, "bongo-loadgen", fmt.Sprintf("seed-%x.state", h.Sum(nil)[:6])), nil
}

// openState creates the file (mode 0600) or resumes an existing one. An
// existing file readable by group or others is refused, since it holds
// passwords.
func openState(path string, want stateHeader) (*seedState, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	st := &seedState{users: map[int]*userState{}, msgs: map[msgKey]msgStatus{}}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- operator-chosen path
	switch {
	case err == nil:
		st.f = f
		if err := st.append(record{T: recHeader, Header: &want}); err != nil {
			_ = f.Close()
			return nil, err
		}
		return st, nil
	case !errors.Is(err, fs.ErrExist):
		return nil, fmt.Errorf("create state file: %w", err)
	}
	f, err = os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0) // #nosec G304 -- operator-chosen path
	if err != nil {
		return nil, fmt.Errorf("open state file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat state file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("state file %s must be a regular file with mode 0600 (it holds passwords), found %v", path, info.Mode())
	}
	data := make([]byte, info.Size())
	if _, err := f.ReadAt(data, 0); err != nil && info.Size() > 0 {
		_ = f.Close()
		return nil, fmt.Errorf("read state file: %w", err)
	}
	st.f = f
	if err := st.load(data, want); err != nil {
		_ = f.Close()
		return nil, err
	}
	return st, nil
}

func (st *seedState) load(data []byte, want stateHeader) error {
	torn := len(data) > 0 && data[len(data)-1] != '\n'
	if torn {
		// A crash can leave half a line. It was never acknowledged, so it is
		// cut off, which keeps the next record on its own line.
		data = data[:bytes.LastIndexByte(data, '\n')+1]
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	first := true
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return fmt.Errorf("state file is corrupt: %w", err)
		}
		if first {
			first = false
			if r.T != recHeader || r.Header == nil {
				return errors.New("state file has no header")
			}
			if d := want.diff(*r.Header); d != "" {
				return fmt.Errorf("state file was made with a different %s; use a new --state-file or the original parameters", d)
			}
			continue
		}
		st.apply(r)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read state file: %w", err)
	}
	if torn {
		if err := st.f.Truncate(int64(len(data))); err != nil {
			return fmt.Errorf("repair state file: %w", err)
		}
	}
	if first {
		// Empty file (crash before the header was written).
		return st.append(record{T: recHeader, Header: &want})
	}
	return nil
}

func (st *seedState) apply(r record) {
	switch r.T {
	case recPending:
		st.users[r.U] = &userState{email: r.Email, pw: r.Pw, pending: true}
	case recUser:
		u := st.users[r.U]
		if u == nil {
			u = &userState{}
			st.users[r.U] = u
		}
		u.done = true
	case recSending:
		if st.msgs[msgKey{r.U, r.M}] != msgDone {
			st.msgs[msgKey{r.U, r.M}] = msgSending
		}
	case recMsg:
		st.msgs[msgKey{r.U, r.M}] = msgDone
	}
}

func (st *seedState) append(r record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode state record: %w", err)
	}
	line = append(line, '\n')
	if _, err := st.f.Write(line); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	if err := st.f.Sync(); err != nil {
		return fmt.Errorf("sync state file: %w", err)
	}
	return nil
}

func (st *seedState) record(r record) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.apply(r)
	return st.append(r)
}

// User returns a copy of the user's state, or the zero value.
func (st *seedState) User(i int) userState {
	st.mu.Lock()
	defer st.mu.Unlock()
	if u := st.users[i]; u != nil {
		return *u
	}
	return userState{}
}

func (st *seedState) Msg(u, m int) msgStatus {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.msgs[msgKey{u, m}]
}

// Pending persists the password BEFORE the account is created, so a crash
// after the server accepted the create still leaves the credential on disk.
func (st *seedState) Pending(i int, email, pw string) error {
	return st.record(record{T: recPending, U: i, Email: email, Pw: pw})
}

func (st *seedState) UserDone(i int) error { return st.record(record{T: recUser, U: i}) }

func (st *seedState) Sending(u, m int) error { return st.record(record{T: recSending, U: u, M: m}) }

func (st *seedState) MsgDone(u, m int) error { return st.record(record{T: recMsg, U: u, M: m}) }

func (st *seedState) Close() error { return st.f.Close() }
