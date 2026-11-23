package credcrypt

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	bongov1 "github.com/bongo-project/bongo/proto/bongo/v1"
)

func randKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func testAccount() *bongov1.FetchAccount {
	return &bongov1.FetchAccount{
		Guid:     "0123456789abcdef0123456789abcdef",
		Protocol: "imap",
		Security: "tls",
		Host:     "imap.example.com",
		Username: "alice@example.com",
	}
}

const testUser = "11111111-2222-3333-4444-555555555555"

func sealed(t *testing.T, kr *Keyring, user string, acct *bongov1.FetchAccount, pw string) *bongov1.FetchAccount {
	t.Helper()
	cred, err := kr.Seal(user, acct, pw)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	out := proto.Clone(acct).(*bongov1.FetchAccount)
	out.Credential = cred
	return out
}

func TestNewKeyringValidates(t *testing.T) {
	good := randKey(t)
	tests := []struct {
		name    string
		cur     []byte
		prev    [][]byte
		wantErr bool
	}{
		{"ok", good, nil, false},
		{"ok with previous", good, [][]byte{randKey(t)}, false},
		{"short current", good[:31], nil, true},
		{"long current", append(append([]byte{}, good...), 0), nil, true},
		{"nil current", nil, nil, true},
		{"short previous", good, [][]byte{good[:16]}, true},
		{"previous equals current", good, [][]byte{good}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kr, err := NewKeyring(tt.cur, tt.prev)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && kr != nil {
				t.Fatal("keyring returned with an error")
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	acct := sealed(t, kr, testUser, testAccount(), "hunter2 äö")
	got, err := kr.Open(testUser, acct)
	if err != nil || got != "hunter2 äö" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	c := acct.Credential
	if c.Version != 1 || c.KeyId != kr.CurrentID() || len(c.Nonce) != 12 || len(c.Ciphertext) == 0 {
		t.Fatalf("credential shape: %+v", c)
	}
	if bytes.Contains(c.Ciphertext, []byte("hunter2")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if kr.NeedsRotation(c) {
		t.Fatal("fresh credential needs rotation")
	}
}

func TestSealRefusesEmptyPassword(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	if _, err := kr.Seal(testUser, testAccount(), ""); err == nil {
		t.Fatal("empty password sealed")
	}
}

func TestTwoSealsDiffer(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	a, _ := kr.Seal(testUser, testAccount(), "same password")
	b, _ := kr.Seal(testUser, testAccount(), "same password")
	if bytes.Equal(a.Nonce, b.Nonce) {
		t.Fatal("nonce reused")
	}
	if bytes.Equal(a.Ciphertext, b.Ciphertext) {
		t.Fatal("ciphertexts equal")
	}
}

// Each field of the associated data must matter: a record moved to another
// user, account, host, protocol, security mode or user name does not open.
func TestOpenFailsWhenBindingChanges(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	base := sealed(t, kr, testUser, testAccount(), "pw")
	tests := []struct {
		name   string
		user   string
		mutate func(a *bongov1.FetchAccount)
	}{
		{"user", "99999999-2222-3333-4444-555555555555", func(*bongov1.FetchAccount) {}},
		{"guid", testUser, func(a *bongov1.FetchAccount) { a.Guid = "fedcba9876543210fedcba9876543210" }},
		{"host", testUser, func(a *bongov1.FetchAccount) { a.Host = "attacker.example" }},
		{"username", testUser, func(a *bongov1.FetchAccount) { a.Username = "bob@example.com" }},
		{"protocol", testUser, func(a *bongov1.FetchAccount) { a.Protocol = "pop3" }},
		{"security", testUser, func(a *bongov1.FetchAccount) { a.Security = "starttls" }},
		{"key id", testUser, func(a *bongov1.FetchAccount) { a.Credential.KeyId = "0000000000000000" }},
		{"flipped ciphertext bit", testUser, func(a *bongov1.FetchAccount) { a.Credential.Ciphertext[0] ^= 1 }},
		{"flipped nonce bit", testUser, func(a *bongov1.FetchAccount) { a.Credential.Nonce[0] ^= 1 }},
		{"truncated ciphertext", testUser, func(a *bongov1.FetchAccount) { a.Credential.Ciphertext = a.Credential.Ciphertext[:3] }},
		{"short nonce", testUser, func(a *bongov1.FetchAccount) { a.Credential.Nonce = a.Credential.Nonce[:8] }},
		{"bad version", testUser, func(a *bongov1.FetchAccount) { a.Credential.Version = 2 }},
		{"no credential", testUser, func(a *bongov1.FetchAccount) { a.Credential = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := proto.Clone(base).(*bongov1.FetchAccount)
			tt.mutate(a)
			pw, err := kr.Open(tt.user, a)
			if err == nil {
				t.Fatalf("opened after changing %s", tt.name)
			}
			if pw != "" {
				t.Fatal("password returned with an error")
			}
			if tt.name != "key id" && !errors.Is(err, ErrCredentialInvalid) {
				t.Fatalf("err = %v, want ErrCredentialInvalid", err)
			}
			if tt.name == "key id" && !errors.Is(err, ErrKeyUnknown) {
				t.Fatalf("err = %v, want ErrKeyUnknown", err)
			}
		})
	}
	// The unchanged record still opens, so every failure above is the change.
	if _, err := kr.Open(testUser, base); err != nil {
		t.Fatal(err)
	}
}

// Fields outside the binding may change without losing the credential.
func TestOpenSurvivesUnboundFields(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	a := sealed(t, kr, testUser, testAccount(), "pw")
	a.Folder, a.Label, a.IntervalMinutes, a.Generation, a.LeaveOnServer, a.Port = "Archive", "work", 60, 9, true, 993
	if _, err := kr.Open(testUser, a); err != nil {
		t.Fatalf("unbound field broke the credential: %v", err)
	}
}

func TestUnknownKeyID(t *testing.T) {
	a, _ := NewKeyring(randKey(t), nil)
	b, _ := NewKeyring(randKey(t), nil)
	acct := sealed(t, a, testUser, testAccount(), "pw")
	if _, err := b.Open(testUser, acct); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("err = %v, want ErrKeyUnknown", err)
	}
	var nilKR *Keyring
	if _, err := nilKR.Open(testUser, acct); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("nil keyring err = %v", err)
	}
	if _, err := nilKR.Seal(testUser, testAccount(), "pw"); err == nil {
		t.Fatal("nil keyring sealed")
	}
}

func TestPreviousKeyOpensAndNeedsRotation(t *testing.T) {
	oldKey, newKey := randKey(t), randKey(t)
	old, _ := NewKeyring(oldKey, nil)
	acct := sealed(t, old, testUser, testAccount(), "rotate me")

	rotated, err := NewKeyring(newKey, [][]byte{oldKey})
	if err != nil {
		t.Fatal(err)
	}
	pw, err := rotated.Open(testUser, acct)
	if err != nil || pw != "rotate me" {
		t.Fatalf("Open under previous key = %q, %v", pw, err)
	}
	if !rotated.NeedsRotation(acct.Credential) {
		t.Fatal("NeedsRotation = false under a previous key")
	}
	again := sealed(t, rotated, testUser, testAccount(), pw)
	if rotated.NeedsRotation(again.Credential) || again.Credential.KeyId != rotated.CurrentID() {
		t.Fatal("re-sealed credential not under the current key")
	}
	if rotated.NeedsRotation(nil) {
		t.Fatal("nil credential needs no rotation")
	}
	// The old keyring cannot open the new credential: ids are distinct.
	if _, err := old.Open(testUser, again); !errors.Is(err, ErrKeyUnknown) {
		t.Fatalf("old keyring opened the new credential: %v", err)
	}
}

func TestKeyIDs(t *testing.T) {
	k1, k2 := randKey(t), randKey(t)
	a, _ := NewKeyring(k1, nil)
	b, _ := NewKeyring(k1, nil)
	c, _ := NewKeyring(k2, nil)
	if a.CurrentID() != b.CurrentID() {
		t.Fatal("id not stable for one key")
	}
	if a.CurrentID() == c.CurrentID() {
		t.Fatal("ids equal for different keys")
	}
	if len(a.CurrentID()) != 16 || strings.Trim(a.CurrentID(), "0123456789abcdef") != "" {
		t.Fatalf("id shape %q", a.CurrentID())
	}
	if strings.Contains(a.CurrentID(), string(k1[:4])) {
		t.Fatal("id leaks the key")
	}
	// The id is not the key's own hex.
	if a.CurrentID() != KeyIDHex(k1) {
		t.Fatal("KeyIDHex disagrees with CurrentID")
	}
}

// Fields are length-prefixed, so moving a byte between neighbours changes the
// associated data.
func TestAADIsLengthPrefixed(t *testing.T) {
	mk := func(host, user string) []byte {
		a := testAccount()
		a.Host, a.Username = host, user
		return aad(testUser, a, "kid")
	}
	if bytes.Equal(mk("ab", "c"), mk("a", "bc")) {
		t.Fatal(`("ab","c") collides with ("a","bc")`)
	}
	if bytes.Equal(mk("a\x00b", "c"), mk("a", "b\x00c")) {
		t.Fatal("NUL smuggles a separator")
	}
	if bytes.Equal(aad("ab", testAccount(), "k"), aad("a", testAccount(), "bk")) {
		t.Fatal("user and key id collide")
	}
	if !bytes.HasPrefix(mk("a", "b"), []byte("bongo/fetch-credential/v1\x00")) {
		t.Fatal("AAD lacks the domain separator")
	}
}

// Errors are fixed text that carries no input.
func TestErrorsCarryNoDetail(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	a := sealed(t, kr, testUser, testAccount(), "topsecret")
	a.Host = "evil.example"
	_, err := kr.Open(testUser, a)
	for _, bad := range []string{"topsecret", "evil.example", testUser, "alice"} {
		if err != nil && strings.Contains(err.Error(), bad) {
			t.Fatalf("error contains %q: %v", bad, err)
		}
	}
}

func TestFixedLengthLimits(t *testing.T) {
	kr, _ := NewKeyring(randKey(t), nil)
	long := strings.Repeat("x", MaxPasswordLen+1)
	if _, err := kr.Seal(testUser, testAccount(), long); err == nil {
		t.Fatal("over-long password sealed")
	}
}

func TestLoopTokens(t *testing.T) {
	cur, prev := randKey(t), randKey(t)
	kr, err := NewKeyring(cur, [][]byte{prev})
	if err != nil {
		t.Fatal(err)
	}
	const g1, g2 = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"

	toks := kr.LoopTokens(g1)
	if len(toks) != 2 {
		t.Fatalf("tokens = %v, want current and previous", toks)
	}
	for _, tok := range toks {
		if len(tok) != 16 {
			t.Fatalf("token %q is not 16 hex chars", tok)
		}
	}
	if toks[0] == toks[1] {
		t.Fatal("current and previous key give one token")
	}
	t.Run("stable", func(t *testing.T) {
		if again := kr.LoopTokens(g1); again[0] != toks[0] || again[1] != toks[1] {
			t.Fatalf("not stable: %v vs %v", again, toks)
		}
	})
	t.Run("differs per guid", func(t *testing.T) {
		other := kr.LoopTokens(g2)
		if other[0] == toks[0] || other[1] == toks[1] {
			t.Fatalf("guid does not change the token: %v vs %v", other, toks)
		}
	})
	t.Run("previous key token is the one a previous-only keyring makes current", func(t *testing.T) {
		old, err := NewKeyring(prev, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := old.LoopTokens(g1); len(got) != 1 || got[0] != toks[1] {
			t.Fatalf("previous-key token = %v, want %q", got, toks[1])
		}
	})
	t.Run("never the key id", func(t *testing.T) {
		for _, tok := range toks {
			if tok == kr.CurrentID() || tok == KeyIDHex(prev) {
				t.Fatalf("token %q equals a key id", tok)
			}
		}
	})
	t.Run("nil keyring", func(t *testing.T) {
		var nilKr *Keyring
		if got := nilKr.LoopTokens(g1); got != nil {
			t.Fatalf("nil keyring tokens = %v", got)
		}
	})
}
