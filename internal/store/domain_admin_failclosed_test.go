package store

import (
	"errors"
	"strings"
	"testing"
)

type fakeKV struct {
	rows []fakeRow
}

type fakeRow struct {
	key, val string
	valErr   error
}

func (f *fakeKV) Get(key []byte) ([]byte, error) {
	for _, r := range f.rows {
		if r.key == string(key) {
			return []byte(r.val), r.valErr
		}
	}
	return nil, ErrNotFound
}

func (f *fakeKV) PrefixScan(prefix []byte) (Iterator, error) {
	var rows []fakeRow
	for _, r := range f.rows {
		if strings.HasPrefix(r.key, string(prefix)) {
			rows = append(rows, r)
		}
	}
	return &fakeIter{rows: rows, idx: -1}, nil
}

type fakeIter struct {
	rows []fakeRow
	idx  int
}

func (i *fakeIter) Next() bool             { i.idx++; return i.idx < len(i.rows) }
func (i *fakeIter) Key() []byte            { return []byte(i.rows[i.idx].key) }
func (i *fakeIter) Close() error           { return nil }
func (i *fakeIter) Value() ([]byte, error) { r := i.rows[i.idx]; return []byte(r.val), r.valErr }

// TestAuthorizedDomains_OwnerReadErrorFailsClosed checks that an unreadable
// owner key surfaces as an error rather than being skipped. The failing key
// sorts before a readable one that the user does own, so a skip-and-continue
// implementation would return a partial, apparently valid, result.
func TestAuthorizedDomains_OwnerReadErrorFailsClosed(t *testing.T) {
	boom := errors.New("value unreadable")
	s := &DomainAdminStore{pebble: &fakeKV{rows: []fakeRow{
		{key: "/system/domain/a.example/owner", valErr: boom},
		{key: "/system/domain/b.example/owner", val: "u1"},
	}}}

	domains, err := s.AuthorizedDomains("u1", false)
	if err == nil {
		t.Fatalf("AuthorizedDomains returned nil error, domains %v", domains)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the read error", err)
	}
	if domains != nil {
		t.Errorf("domains = %v, want nil on error", domains)
	}

	// Control: with no read error the same data resolves normally.
	ok := &DomainAdminStore{pebble: &fakeKV{rows: []fakeRow{
		{key: "/system/domain/a.example/owner", val: "u2"},
		{key: "/system/domain/b.example", val: "enabled"},
		{key: "/system/domain/b.example/owner", val: "u1"},
	}}}
	got, err := ok.AuthorizedDomains("u1", false)
	if err != nil || len(got) != 1 || got[0] != "b.example" {
		t.Errorf("control: got %v, %v; want [b.example], nil", got, err)
	}
}

func TestAuthorizedDomains_DomainExistenceReadErrorFailsClosed(t *testing.T) {
	boom := errors.New("pebble read failed")
	s := &DomainAdminStore{pebble: &fakeKV{rows: []fakeRow{
		{key: "/system/domain_admin/a.example/u1", val: "1"},
		{key: "/system/domain/a.example", val: "enabled", valErr: boom},
	}}}
	domains, err := s.AuthorizedDomains("u1", false)
	if err == nil {
		t.Fatalf("AuthorizedDomains returned nil error, domains %v", domains)
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap the read error", err)
	}
	if domains != nil {
		t.Errorf("domains = %v, want nil on error", domains)
	}
}
