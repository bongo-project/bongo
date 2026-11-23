package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// FetchConfig configures collecting mail from a user's remote POP3 and IMAP
// accounts. The block is optional; without a credentials key the feature is
// off, because the key is what protects the stored third-party passwords.
type FetchConfig struct {
	// CredentialsKey is standard base64 (padding optional) of exactly 32
	// random bytes: `openssl rand -base64 32`. It encrypts the stored remote
	// passwords. BONGO_FETCH_CREDENTIALS_KEY overrides it. Every node of a
	// cluster must carry the same keys. Restart to change.
	CredentialsKey string `hcl:"credentials_key,optional" mapstructure:"credentials_key"`
	// PreviousCredentialsKeys are keys that only decrypt, kept while stored
	// passwords are re-encrypted under the current key after a rotation.
	PreviousCredentialsKeys []string `hcl:"previous_credentials_keys,optional" mapstructure:"previous_credentials_keys"`
	// AllowPrivateNetworks lets a remote account name a host on loopback, a
	// private or a link-local address. Off by default: the host is typed by a
	// user, so on an internet-facing server it is a way to reach the internal
	// network. It lifts the address rule only; the port rule stays.
	AllowPrivateNetworks bool `hcl:"allow_private_networks,optional" mapstructure:"allow_private_networks"`
}

// fetchKeyLen is the decoded length of every credentials key.
const fetchKeyLen = 32

// FetchAllowPrivateNetworks reports fetch.allow_private_networks; an absent
// block means false.
func (c *Config) FetchAllowPrivateNetworks() bool {
	return c != nil && c.Fetch != nil && c.Fetch.AllowPrivateNetworks
}

// decodeFetchKey decodes one key. The error never contains the value.
func decodeFetchKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return nil, errors.New("must be standard base64")
	}
	if len(b) != fetchKeyLen {
		return nil, fmt.Errorf("must decode to exactly %d bytes", fetchKeyLen)
	}
	return b, nil
}

// validateFetch checks the shape of the keys. Every error names the key, never
// its value.
func (c *Config) validateFetch() error {
	f := c.Fetch
	if f == nil {
		return nil
	}
	var current []byte
	if strings.TrimSpace(f.CredentialsKey) != "" {
		var err error
		if current, err = decodeFetchKey(f.CredentialsKey); err != nil {
			return fmt.Errorf("fetch.credentials_key: %w", err)
		}
	}
	if len(f.PreviousCredentialsKeys) > 0 && current == nil {
		return errors.New("fetch.previous_credentials_keys: requires fetch.credentials_key")
	}
	seen := make([][]byte, 0, len(f.PreviousCredentialsKeys))
	for i, p := range f.PreviousCredentialsKeys {
		pk, err := decodeFetchKey(p)
		if err != nil {
			return fmt.Errorf("fetch.previous_credentials_keys[%d]: %w", i, err)
		}
		if bytes.Equal(pk, current) {
			return fmt.Errorf("fetch.previous_credentials_keys[%d]: must not equal fetch.credentials_key", i)
		}
		for _, o := range seen {
			if bytes.Equal(o, pk) {
				return fmt.Errorf("fetch.previous_credentials_keys[%d]: duplicate key", i)
			}
		}
		seen = append(seen, pk)
	}
	if current != nil && c.Web.SessionSecret != "" {
		// The two secrets must be independent. Compare as the raw bytes and as
		// the base64 the operator may have reused.
		secret := []byte(c.Web.SessionSecret)
		if bytes.Equal(current, secret) {
			return errors.New("fetch.credentials_key: must differ from web.session_secret")
		}
		if sb, err := decodeFetchKey(c.Web.SessionSecret); err == nil && bytes.Equal(current, sb) {
			return errors.New("fetch.credentials_key: must differ from web.session_secret")
		}
	}
	return nil
}

// FetchKeys returns the decoded current key and the decrypt-only previous
// keys, as copies. A nil block, an empty key or an undecodable key (Validate
// refuses those) returns a nil current key: fetch is off.
func (c *Config) FetchKeys() (current []byte, previous [][]byte) {
	if c == nil || c.Fetch == nil || strings.TrimSpace(c.Fetch.CredentialsKey) == "" {
		return nil, nil
	}
	cur, err := decodeFetchKey(c.Fetch.CredentialsKey)
	if err != nil {
		return nil, nil
	}
	for _, p := range c.Fetch.PreviousCredentialsKeys {
		if pk, err := decodeFetchKey(p); err == nil {
			previous = append(previous, pk)
		}
	}
	return cur, previous
}

// ResolveFetchKeys is FetchKeys plus the dev rule: in the dev role with no
// configured key it returns a fresh random key and ephemeral true. Dev storage
// is ephemeral, so credentials sealed under a throwaway key are lost with the
// rest of the data. Outside dev, no key means fetch is off.
func (c *Config) ResolveFetchKeys() (current []byte, previous [][]byte, ephemeral bool, err error) {
	current, previous = c.FetchKeys()
	if current != nil || c == nil || c.Server.Role != "dev" {
		return current, previous, false, nil
	}
	key := make([]byte, fetchKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, nil, false, fmt.Errorf("generating fetch credentials key: %w", err)
	}
	return key, nil, true, nil
}

// ValidateFetchKeyPlacement refuses a config file that sits inside
// storage.data_dir while holding key material: the data directory is what
// backups and snapshots copy, and the key must never travel with the data it
// protects. It judges the file's own fetch block, read afresh, so a key given
// by BONGO_FETCH_CREDENTIALS_KEY does not excuse a different key (or any
// previous key) written in the file. An empty configFile (defaults only) is
// fine.
func (c *Config) ValidateFetchKeyPlacement(configFile string) error {
	if configFile == "" || c.Storage.DataDir == "" || !pathInside(configFile, c.Storage.DataDir) {
		return nil
	}
	fileCfg, _, err := LoadFile(configFile)
	if err != nil || fileCfg.Fetch == nil {
		return nil // the file was loaded once already; nothing to judge
	}
	f := fileCfg.Fetch
	if strings.TrimSpace(f.CredentialsKey) == "" && len(f.PreviousCredentialsKeys) == 0 {
		return nil
	}
	return errors.New("fetch: the credentials keys are in a config file inside storage.data_dir, which backups copy; " +
		"move the file out of the data directory or set BONGO_FETCH_CREDENTIALS_KEY instead and remove the keys from the file")
}

// pathInside reports whether path lies inside dir, after making both absolute
// and resolving symbolic links as far as the paths exist.
func pathInside(path, dir string) bool {
	p, d := resolveLoose(path), resolveLoose(dir)
	rel, err := filepath.Rel(d, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// resolveLoose makes p absolute and evaluates symbolic links in its longest
// existing prefix; the remaining, not yet existing tail is appended as is.
func resolveLoose(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	rest := ""
	cur := abs
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
