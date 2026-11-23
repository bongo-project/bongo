package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

const fetchTestKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

// Startup refuses keys in a config file inside the data directory, and
// accepts the same file when the current key comes from the environment and
// no previous key is listed.
func TestLoadServeConfigFetchKeyPlacement(t *testing.T) {
	tests := []struct {
		name    string
		inData  bool
		env     string
		body    string
		wantErr bool
	}{
		{"key in file inside data dir", true, "", `fetch { credentials_key = "` + fetchTestKey + `" }`, true},
		{"key in file outside data dir", false, "", `fetch { credentials_key = "` + fetchTestKey + `" }`, false},
		{"env key, file inside data dir with no fetch block", true, fetchTestKey, ``, false},
		// The environment key must not excuse a different key in the file.
		{"env key, own key in file inside data dir", true, fetchTestKey,
			`fetch { credentials_key = "MTIzNDU2Nzg5MGFiY2RlZjEyMzQ1Njc4OTBhYmNkZWY=" }`, true},
		{"env key, previous key in file inside data dir", true, fetchTestKey,
			`fetch { previous_credentials_keys = ["MTIzNDU2Nzg5MGFiY2RlZjEyMzQ1Njc4OTBhYmNkZWY="] }`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BONGO_CONFIG", "")
			t.Setenv("BONGO_ROLE", "")
			t.Setenv("BONGO_BIND", "")
			t.Setenv("BONGO_JOIN", "")
			t.Setenv("BONGO_LOG-LEVEL", "")
			t.Setenv("BONGO_FETCH_CREDENTIALS_KEY", tt.env)
			root := t.TempDir()
			data := filepath.Join(root, "data")
			if err := os.MkdirAll(data, 0o700); err != nil {
				t.Fatal(err)
			}
			dir := root
			if tt.inData {
				dir = data
			}
			path := filepath.Join(dir, "bongo.hcl")
			src, err := os.ReadFile("../../deploy/bongo.example.hcl")
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(string(src), `data_dir = "/var/lib/bongo/data"`, `data_dir = "`+data+`"`, 1) + "\n" + tt.body + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			v := viper.New()
			v.Set("config", path)
			_, _, _, err = loadServeConfig(v)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), fetchTestKey) {
				t.Fatal("error contains the key")
			}
		})
	}
}
