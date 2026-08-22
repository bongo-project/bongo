package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestExampleDNSBLBlockParsesWhenUncommented is a documentation contract
// test. The dnsbl block in deploy/bongo.example.hcl ships commented out, so
// TestShippedConfigsPassValidate parses right past it -- the documented
// syntax itself is never exercised. An operator uncommenting it is the first
// person to find out whether it is correct, which is the worst time.
//
// This uncomments the block, parses the result, and asserts the documented
// defaults are the ones the code actually applies.
func TestExampleDNSBLBlockParsesWhenUncommented(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "bongo.example.hcl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	uncommented, found := uncommentBlock(string(raw), "dnsbl {")
	if !found {
		t.Fatal("no commented-out `# dnsbl {` block found in deploy/bongo.example.hcl -- if the block was renamed or uncommented, update this test rather than deleting it")
	}

	tmp := filepath.Join(t.TempDir(), "uncommented.hcl")
	if err := os.WriteFile(tmp, []byte(uncommented), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	cfg, err := LoadFromFile(tmp)
	if err != nil {
		t.Fatalf("the documented dnsbl block does not parse once uncommented: %v", err)
	}
	if err := cfg.Antispam.DNSBL.Validate(); err != nil {
		t.Fatalf("the documented dnsbl block fails validation: %v", err)
	}

	// The comments promise these defaults. If a default changes in code and
	// not in the example, this is what catches it.
	if got := cfg.Antispam.DNSBLTimeout(); got.String() != "5s" {
		t.Errorf("timeout default = %v, but the example documents 5s", got)
	}
	if got := cfg.Antispam.DNSBLCacheTTL(); got.String() != "10m0s" {
		t.Errorf("cache_ttl default = %v, but the example documents 10m", got)
	}

	zones := cfg.Antispam.DNSBLZones()
	if len(zones) == 0 {
		t.Fatal("documented block produced no zones")
	}
	// The example's last zone deliberately omits weight to demonstrate the
	// default; the comment says that default is 0.25.
	last := zones[len(zones)-1]
	if last.Weight != 0.25 {
		t.Errorf("zone with omitted weight got %v, but the example documents a 0.25 default", last.Weight)
	}
	// Placeholder zones only: naming a real blocklist in shipped config is a
	// soft endorsement and carries that provider's terms of use.
	for _, z := range zones {
		if !strings.HasSuffix(z.Name, ".invalid") {
			t.Errorf("example zone %q is not a placeholder -- shipped config must not name a real blocklist", z.Name)
		}
	}
}

// uncommentBlock strips the leading comment marker from the commented-out
// HCL block introduced by "# "+opener, up to and including its closing "# }"
// at the same indentation. Inner lines that are genuinely prose comments
// stay commented, because they keep their own second "#" after the first is
// removed.
func uncommentBlock(src, opener string) (string, bool) {
	lines := strings.Split(src, "\n")
	marker := regexp.MustCompile(`^(\s*)#\s?(.*)$`)

	out := make([]string, 0, len(lines))
	inBlock, found := false, false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && trimmed == "# "+opener {
			inBlock, found = true, true
		}
		if inBlock {
			if m := marker.FindStringSubmatch(line); m != nil {
				out = append(out, m[1]+m[2])
			} else {
				out = append(out, line)
			}
			if trimmed == "# }" {
				inBlock = false
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), found
}
