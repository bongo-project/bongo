package config

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func b64(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func fetchCfg(current string, previous ...string) *Config {
	c := Defaults()
	c.Fetch = &FetchConfig{CredentialsKey: current, PreviousCredentialsKeys: previous}
	return c
}

func TestFetchKeyValidation(t *testing.T) {
	good := b64(32)
	other := b64(32)
	tests := []struct {
		name    string
		cfg     func() *Config
		secret  string
		wantErr string
		secrets []string // strings that must never appear in the error
	}{
		{name: "no block", cfg: func() *Config { return Defaults() }},
		{name: "empty block", cfg: func() *Config { return fetchCfg("") }},
		{name: "good", cfg: func() *Config { return fetchCfg(good) }},
		{name: "good unpadded", cfg: func() *Config {
			return fetchCfg(strings.TrimRight(b64(32), "="))
		}},
		{name: "good with previous", cfg: func() *Config { return fetchCfg(good, other) }},
		{name: "bad base64", cfg: func() *Config { return fetchCfg("not*base64*at*all!!") }, wantErr: "fetch.credentials_key", secrets: []string{"not*base64*at*all!!"}},
		{name: "31 bytes", cfg: func() *Config { return fetchCfg(b64(31)) }, wantErr: "32 bytes"},
		{name: "33 bytes", cfg: func() *Config { return fetchCfg(b64(33)) }, wantErr: "32 bytes"},
		{name: "bad previous", cfg: func() *Config { return fetchCfg(good, "###") }, wantErr: "previous_credentials_keys"},
		{name: "short previous", cfg: func() *Config { return fetchCfg(good, b64(16)) }, wantErr: "previous_credentials_keys"},
		{name: "current equals previous", cfg: func() *Config { return fetchCfg(good, other, good) }, wantErr: "must not equal"},
		{name: "duplicate previous", cfg: func() *Config { return fetchCfg(good, other, other) }, wantErr: "duplicate"},
		{name: "previous without current", cfg: func() *Config { return fetchCfg("", other) }, wantErr: "requires fetch.credentials_key"},
		{name: "equals session secret raw", cfg: func() *Config {
			c := fetchCfg(good)
			return c
		}, secret: string(mustDecode(t, good)), wantErr: "session_secret"},
		{name: "equals session secret base64", cfg: func() *Config { return fetchCfg(good) }, secret: good, wantErr: "session_secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.cfg()
			if tt.secret != "" {
				c.Web.SessionSecret = tt.secret
			}
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate = %v, want error containing %q", err, tt.wantErr)
			}
			msg := err.Error()
			if c.Fetch != nil {
				for _, k := range append([]string{c.Fetch.CredentialsKey}, c.Fetch.PreviousCredentialsKeys...) {
					if k != "" && strings.Contains(msg, k) {
						t.Fatalf("error contains a key value: %v", err)
					}
				}
			}
			if tt.secret != "" && strings.Contains(msg, tt.secret) {
				t.Fatalf("error contains the session secret")
			}
		})
	}
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFetchKeys(t *testing.T) {
	cur, prev := Defaults().FetchKeys()
	if cur != nil || prev != nil {
		t.Fatal("no block means no keys")
	}
	var nilCfg *Config
	if cur, _ := nilCfg.FetchKeys(); cur != nil {
		t.Fatal("nil config means no keys")
	}
	a, b := b64(32), strings.TrimRight(b64(32), "=")
	c := fetchCfg(a, b)
	cur, prev = c.FetchKeys()
	if !bytes.Equal(cur, mustDecode(t, a)) || len(prev) != 1 || len(prev[0]) != 32 {
		t.Fatalf("FetchKeys = %x, %x", cur, prev)
	}
	// The returned key is a copy: the config string cannot be changed
	// through it.
	cur[0] ^= 0xff
	cur2, _ := c.FetchKeys()
	if bytes.Equal(cur, cur2) {
		t.Fatal("FetchKeys shares storage")
	}
}

func TestResolveFetchKeysDev(t *testing.T) {
	dev := Defaults()
	dev.Server.Role = "dev"
	cur, _, ephemeral, err := dev.ResolveFetchKeys()
	if err != nil || len(cur) != 32 || !ephemeral {
		t.Fatalf("dev without key = %d bytes, ephemeral %v, %v", len(cur), ephemeral, err)
	}
	cur2, _, _, _ := dev.ResolveFetchKeys()
	if bytes.Equal(cur, cur2) {
		t.Fatal("two dev keys equal")
	}

	srv := Defaults()
	srv.Server.Role = "server"
	cur, _, ephemeral, err = srv.ResolveFetchKeys()
	if err != nil || cur != nil || ephemeral {
		t.Fatalf("server without key = %v %v %v", cur, ephemeral, err)
	}

	dev = fetchCfg(b64(32))
	dev.Server.Role = "dev"
	_, _, ephemeral, _ = dev.ResolveFetchKeys()
	if ephemeral {
		t.Fatal("a configured key must be used in dev")
	}
}

func TestFetchEnvOverrideWins(t *testing.T) {
	fileKey, envKey := b64(32), b64(32)
	t.Setenv("BONGO_FETCH_CREDENTIALS_KEY", envKey)
	v := viper.New()
	cfg := fetchCfg(fileKey)
	ApplyOverrides(v, cfg)
	if cfg.Fetch.CredentialsKey != envKey {
		t.Fatal("env did not override the file")
	}

	// With no block in the file, the env still switches fetch on.
	v = viper.New()
	cfg = Defaults()
	ApplyOverrides(v, cfg)
	if cfg.Fetch == nil || cfg.Fetch.CredentialsKey != envKey {
		t.Fatal("env did not create the block")
	}
	if cur, _ := cfg.FetchKeys(); !bytes.Equal(cur, mustDecode(t, envKey)) {
		t.Fatal("FetchKeys does not return the env key")
	}
}

func TestFetchEnvUnsetLeavesConfigAlone(t *testing.T) {
	t.Setenv("BONGO_FETCH_CREDENTIALS_KEY", "")
	cfg := Defaults()
	ApplyOverrides(viper.New(), cfg)
	if cfg.Fetch != nil {
		t.Fatal("block created without a key")
	}
}

func TestFetchKeyPlacement(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	for _, d := range []string{filepath.Join(dataDir, "conf"), filepath.Join(root, "etc"), filepath.Join(root, "data2")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dataDir, link); err != nil {
		t.Fatal(err)
	}
	example, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bongo.example.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	key, other := b64(32), b64(32)
	withKey := `fetch { credentials_key = "` + key + `" }`
	withPrevious := `fetch { previous_credentials_keys = ["` + other + `"] }`
	tests := []struct {
		name    string
		file    string // path of the config file, created with body appended to the example
		body    string
		wantErr bool
	}{
		{"file inside data dir", filepath.Join(dataDir, "conf", "bongo.hcl"), withKey, true},
		{"file directly in data dir", filepath.Join(dataDir, "bongo.hcl"), withKey, true},
		{"file inside via symlink", filepath.Join(link, "link.hcl"), withKey, true},
		{"file inside, dot segments", filepath.Join(root, "etc", "..", "data", "dots.hcl"), withKey, true},
		{"previous key only, inside", filepath.Join(dataDir, "prev.hcl"), withPrevious, true},
		{"file outside", filepath.Join(root, "etc", "bongo.hcl"), withKey, false},
		{"sibling with the same prefix", filepath.Join(root, "data2", "bongo.hcl"), withKey, false},
		{"inside, no fetch block", filepath.Join(dataDir, "plain.hcl"), "", false},
		{"inside, empty fetch block", filepath.Join(dataDir, "empty.hcl"), `fetch { allow_private_networks = true }`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(tt.file, append(append([]byte{}, example...), []byte("\n"+tt.body+"\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
			// The effective config may carry a key from the environment that
			// differs from the file's; that must not excuse the file.
			c := fetchCfg(b64(32))
			c.Storage.DataDir = dataDir
			err := c.ValidateFetchKeyPlacement(tt.file)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && (strings.Contains(err.Error(), key) || strings.Contains(err.Error(), other)) {
				t.Fatal("error contains a key")
			}
		})
	}
	t.Run("no config file", func(t *testing.T) {
		c := fetchCfg(key)
		c.Storage.DataDir = dataDir
		if err := c.ValidateFetchKeyPlacement(""); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFetchKeysAreRestartAndSecret(t *testing.T) {
	for _, k := range []string{"fetch.credentials_key", "fetch.previous_credentials_keys", "fetch.allow_private_networks"} {
		class, ok := ClassOf(k)
		if !ok || class != ClassRestart {
			t.Errorf("%s: class = %v, ok = %v; want restart", k, class, ok)
		}
	}
	for k, want := range map[string]bool{
		"fetch.credentials_key": true, "fetch.previous_credentials_keys": true, "fetch.allow_private_networks": false,
	} {
		if got := keyClasses[k].Secret; got != want {
			t.Errorf("%s: secret = %v, want %v", k, got, want)
		}
	}
	a, b := b64(32), b64(32)
	_, restart, _ := Diff(fetchCfg(a), fetchCfg(b))
	if len(restart) != 1 || restart[0].Key != "fetch.credentials_key" || !restart[0].Secret {
		t.Fatalf("diff = %+v", restart)
	}
}

func TestFetchBlockSurvivesClone(t *testing.T) {
	c := fetchCfg(b64(32), b64(32))
	c.Fetch.AllowPrivateNetworks = true
	d := Clone(c)
	if d.Fetch == c.Fetch || d.Fetch.CredentialsKey != c.Fetch.CredentialsKey ||
		len(d.Fetch.PreviousCredentialsKeys) != 1 || !d.Fetch.AllowPrivateNetworks {
		t.Fatal("clone lost the block")
	}
	if !c.FetchAllowPrivateNetworks() || Defaults().FetchAllowPrivateNetworks() {
		t.Fatal("FetchAllowPrivateNetworks")
	}
}

// The shipped example documents the block; once uncommented it must decode,
// and a file without the block must keep decoding.
func TestExampleFetchBlockParses(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "bongo.example.hcl"))
	if err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(t.TempDir(), "plain.hcl")
	if err := os.WriteFile(plain, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(plain)
	if err != nil {
		t.Fatalf("example without the fetch block: %v", err)
	}
	if cfg.Fetch != nil {
		t.Fatal("commented-out block must leave fetch off")
	}

	uncommented, found := uncommentBlock(string(raw), "fetch {")
	if !found {
		t.Fatal("no commented-out `# fetch {` block in deploy/bongo.example.hcl")
	}
	for _, key := range []string{"credentials_key", "previous_credentials_keys", "allow_private_networks"} {
		if !strings.Contains(uncommented, key) {
			t.Fatalf("documented block lacks %s", key)
		}
	}
	tmp := filepath.Join(t.TempDir(), "uncommented.hcl")
	if err := os.WriteFile(tmp, []byte(uncommented), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFromFile(tmp)
	if err != nil {
		t.Fatalf("documented fetch block does not parse: %v", err)
	}
	if cfg.Fetch == nil || !cfg.Fetch.AllowPrivateNetworks {
		t.Fatal("documented block did not decode")
	}
	if strings.Contains(uncommented, "credentials_key") && cfg.Fetch.CredentialsKey == "" {
		t.Fatal("documented key was not read")
	}
}
