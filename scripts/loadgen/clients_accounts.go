package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// account is one seeded user. The password is a Password so no formatting
// route can print it; Reveal is called only where a login is sent.
type account struct {
	index int
	email string
	pw    Password
}

// Formatting an account names its index only. fmt cannot call a method on an
// unexported field, so without this a %v of an account would print the
// password inside its Password.
func (a account) String() string   { return fmt.Sprintf("account %d", a.index) }
func (a account) GoString() string { return a.String() }
func (a account) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(a.String()))
}

// maxStateFile bounds what loadSeedAccounts reads; a state file of 100000
// users is a few tens of megabytes.
const maxStateFile = 256 << 20

// loadSeedAccounts reads the accounts a seed run finished creating. Unlike
// openState it never creates, appends to or repairs the file: clients only
// reads what seed wrote, and a torn last line (a crash mid-write) is ignored.
// The file must be a regular file with mode 0600, since it holds passwords,
// and every address must lie inside the test domain, so a state file from
// another deployment cannot point load at real accounts. Errors name a user
// index or a line number, never an address or password.
func loadSeedAccounts(path, testDomain string) ([]account, error) {
	f, err := os.Open(path) // #nosec G304 -- operator-chosen path
	if err != nil {
		return nil, errors.New("open state file: " + errKind(err))
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, errors.New("stat state file: " + errKind(err))
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("state file must be a regular file with mode 0600 (it holds passwords), found %v", info.Mode())
	}
	if info.Size() > maxStateFile {
		return nil, errors.New("state file is too large")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStateFile+1))
	if err != nil {
		return nil, errors.New("read state file: " + errKind(err))
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = data[:bytes.LastIndexByte(data, '\n')+1]
	}
	st := &seedState{users: map[int]*userState{}, msgs: map[msgKey]msgStatus{}}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for line := 1; sc.Scan(); line++ {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("state file line %d is not valid", line)
		}
		if line == 1 {
			if r.T != recHeader || r.Header == nil {
				return nil, errors.New("state file has no header")
			}
			continue
		}
		st.apply(r)
	}
	if err := sc.Err(); err != nil {
		return nil, errors.New("read state file: " + errKind(err))
	}
	var out []account
	for i, u := range st.users {
		if !u.done || u.pw == "" {
			continue
		}
		if err := checkAddress(u.email, testDomain); err != nil {
			return nil, fmt.Errorf("state file user %d is outside the test domain", i)
		}
		out = append(out, account{index: i, email: u.email, pw: Password{v: u.pw}})
	}
	if len(out) == 0 {
		return nil, errors.New("state file holds no finished accounts: run seed first")
	}
	sort.Slice(out, func(a, b int) bool { return out[a].index < out[b].index })
	return out, nil
}

// errKind names a file error without its path.
func errKind(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "no such file"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	}
	return "failed"
}
