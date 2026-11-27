package main

import (
	"strings"
	"testing"
)

const goodID = "0123456789abcdef0123456789abcdef"

func TestParseCheck(t *testing.T) {
	long := "1.0.0-" + strings.Repeat("a", 58) // 64 characters
	tests := []struct {
		name    string
		body    string
		ok      bool
		version string
		id      string
	}{
		{"plain", `{"version":"0.17.0","instance_id":"` + goodID + `"}`, true, "0.17.0", goodID},
		{"leading v stripped", `{"version":"v0.17.0","instance_id":"` + goodID + `"}`, true, "0.17.0", goodID},
		{"pre-release", `{"version":"0.17.0-rc.1","instance_id":"` + goodID + `"}`, true, "0.17.0-rc.1", goodID},
		{"upper-case id lowered", `{"version":"1.2.3","instance_id":"` + strings.ToUpper(goodID) + `"}`, true, "1.2.3", goodID},
		{"surrounding whitespace", " \n{\"version\":\"1.2.3\",\"instance_id\":\"" + goodID + "\"}\n", true, "1.2.3", goodID},
		{"key order swapped", `{"instance_id":"` + goodID + `","version":"1.2.3"}`, true, "1.2.3", goodID},
		{"duplicate key last wins", `{"version":"bad","version":"1.2.3","instance_id":"` + goodID + `"}`, true, "1.2.3", goodID},
		{"64 characters", `{"version":"` + long + `","instance_id":"` + goodID + `"}`, true, long, goodID},

		{"invalid UTF-8 in version", "{\"version\":\"1.2.3\xff\",\"instance_id\":\"" + goodID + "\"}", false, "", ""},
		{"invalid UTF-8 in id", "{\"version\":\"1.2.3\",\"instance_id\":\"" + goodID[:31] + "\xff\"}", false, "", ""},
		{"invalid UTF-8 in key", "{\"version\":\"1.2.3\",\"instance_id\":\"" + goodID + "\",\"\xff\":\"x\"}", false, "", ""},
		{"65 characters", `{"version":"` + long + `a","instance_id":"` + goodID + `"}`, false, "", ""},
		{"capitalised key", `{"Version":"1.2.3","instance_id":"` + goodID + `"}`, false, "", ""},
		{"capitalised id key", `{"version":"1.2.3","Instance_ID":"` + goodID + `"}`, false, "", ""},
		{"extra key", `{"version":"1.2.3","instance_id":"` + goodID + `","x":"y"}`, false, "", ""},
		{"missing id", `{"version":"1.2.3"}`, false, "", ""},
		{"missing version", `{"instance_id":"` + goodID + `"}`, false, "", ""},
		{"null version", `{"version":null,"instance_id":"` + goodID + `"}`, false, "", ""},
		{"null id", `{"version":"1.2.3","instance_id":null}`, false, "", ""},
		{"number version", `{"version":1,"instance_id":"` + goodID + `"}`, false, "", ""},
		{"array body", `[]`, false, "", ""},
		{"null body", `null`, false, "", ""},
		{"string body", `"x"`, false, "", ""},
		{"empty body", ``, false, "", ""},
		{"trailing garbage", `{"version":"1.2.3","instance_id":"` + goodID + `"} x`, false, "", ""},
		{"second object", `{"version":"1.2.3","instance_id":"` + goodID + `"}{}`, false, "", ""},
		{"invalid UTF-8", "{\"version\":\"1.2.3\",\"instance_id\":\"" + goodID + "\",\"x\":\"\xff\"}", false, "", ""},
		{"invalid UTF-8 in value", "{\"version\":\"1.2.3\xff\",\"instance_id\":\"" + goodID + "\"}", false, "", ""},
		{"dev", `{"version":"dev","instance_id":"` + goodID + `"}`, false, "", ""},
		{"empty version", `{"version":"","instance_id":"` + goodID + `"}`, false, "", ""},
		{"two v", `{"version":"vv1.2.3","instance_id":"` + goodID + `"}`, false, "", ""},
		{"build metadata", `{"version":"1.2.3+build","instance_id":"` + goodID + `"}`, false, "", ""},
		{"leading zero", `{"version":"01.2.3","instance_id":"` + goodID + `"}`, false, "", ""},
		{"31 hex", `{"version":"1.2.3","instance_id":"` + goodID[:31] + `"}`, false, "", ""},
		{"33 hex", `{"version":"1.2.3","instance_id":"` + goodID + `0"}`, false, "", ""},
		{"non-hex", `{"version":"1.2.3","instance_id":"` + strings.Repeat("g", 32) + `"}`, false, "", ""},
		{"trailing newline in id", `{"version":"1.2.3","instance_id":"` + goodID + `\n"}`, false, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCheck([]byte(tc.body))
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && (got.version != tc.version || got.id != tc.id) {
				t.Errorf("got %+v, want version %q id %q", got, tc.version, tc.id)
			}
		})
	}
}

func TestParseCheckSizeBoundary(t *testing.T) {
	pad := func(n int) []byte {
		base := `{"version":"1.2.3","instance_id":"` + goodID + `"}`
		return []byte(base + strings.Repeat(" ", n-len(base)))
	}
	if _, ok := parseCheck(pad(1024)); !ok {
		t.Error("1024 bytes refused")
	}
	if _, ok := parseCheck(pad(1025)); ok {
		t.Error("1025 bytes accepted")
	}
}
