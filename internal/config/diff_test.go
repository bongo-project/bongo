package config

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// This file guards the reload classification table in diff.go. Classifying a
// key (hot, restart, noop, secret) is a judgement call that no test can make.
// What the tests do is make an unclassified key impossible: adding a leaf to
// a Config struct without classifying it fails TestEveryConfigLeafIsClassified,
// the same way knownBlobBearingFields guards blob references in
// internal/agent/blobref_enumeration_test.go. A reviewer must read any new
// table entry and decide whether the class is right.

// hclName returns the key name from an hcl tag, or "" if the field has none.
func hclName(f reflect.StructField) string {
	tag := f.Tag.Get("hcl")
	if tag == "" || tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// configLeaves walks the Config type independently of Diff's walker.
func configLeaves(t *testing.T) []string {
	t.Helper()
	var leaves []string
	var walk func(prefix string, ty reflect.Type)
	walk = func(prefix string, ty reflect.Type) {
		for i := range ty.NumField() {
			f := ty.Field(i)
			name := hclName(f)
			if name == "" {
				t.Errorf("%s.%s has no usable hcl tag", prefix, f.Name)
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			ft := f.Type
			switch {
			case ft.Kind() == reflect.Struct:
				walk(path, ft)
			case ft.Kind() == reflect.Pointer && ft.Elem().Kind() == reflect.Struct:
				walk(path, ft.Elem())
			case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
				walk(path, ft.Elem())
			case ft.Kind() == reflect.String, ft.Kind() == reflect.Int, ft.Kind() == reflect.Bool,
				ft.Kind() == reflect.Float64,
				ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String:
				leaves = append(leaves, path)
			default:
				t.Errorf("%s has unsupported kind %s; extend the walkers", path, ft)
			}
		}
	}
	walk("", reflect.TypeOf(Config{}))
	return leaves
}

func TestEveryConfigLeafIsClassified(t *testing.T) {
	leaves := configLeaves(t)
	seen := map[string]bool{}
	for _, l := range leaves {
		seen[l] = true
		kc, ok := keyClasses[l]
		if !ok || kc.Class == ClassUnclassified {
			t.Errorf("config key %q is not classified in keyClasses (diff.go)", l)
		}
	}
	for k := range keyClasses {
		if !seen[k] {
			t.Errorf("keyClasses has stale entry %q matching no Config leaf", k)
		}
	}
}

func TestSecretLookingKeysAreFlagged(t *testing.T) {
	// Exemptions need a stated reason. None today.
	notSecret := map[string]string{}
	for _, l := range configLeaves(t) {
		_, last := lastSegment(l)
		if strings.HasSuffix(last, "_file") {
			continue
		}
		looks := strings.Contains(last, "secret") || strings.Contains(last, "password") ||
			strings.Contains(last, "token") || strings.HasSuffix(last, "_key") || last == "key"
		if !looks {
			continue
		}
		if reason, ok := notSecret[l]; ok && reason != "" {
			continue
		}
		if !keyClasses[l].Secret {
			t.Errorf("key %q looks secret but is not flagged Secret", l)
		}
	}
	for _, k := range secretKeys {
		if !keyClasses[k].Secret {
			t.Errorf("secretKeys entry %q not flagged", k)
		}
	}
}

func lastSegment(p string) (string, string) {
	i := strings.LastIndex(p, ".")
	return p[:i+1], p[i+1:]
}

// setLeaf sets the leaf at path on cfg to a value distinct from its current
// one, allocating pointer blocks and appending one zero element to repeated
// blocks as it goes.
func setLeaf(t *testing.T, v reflect.Value, parts []string) {
	t.Helper()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if hclName(f) != parts[0] {
			continue
		}
		fv := v.Field(i)
		if len(parts) > 1 {
			switch fv.Kind() {
			case reflect.Pointer:
				if fv.IsNil() {
					fv.Set(reflect.New(fv.Type().Elem()))
				}
				setLeaf(t, fv.Elem(), parts[1:])
			case reflect.Slice:
				fv.Set(reflect.Append(fv, reflect.Zero(fv.Type().Elem())))
				setLeaf(t, fv.Index(fv.Len()-1), parts[1:])
			default:
				setLeaf(t, fv, parts[1:])
			}
			return
		}
		switch fv.Kind() {
		case reflect.String:
			fv.SetString("diff-sentinel")
		case reflect.Int:
			fv.SetInt(fv.Int() + 1)
		case reflect.Bool:
			fv.SetBool(!fv.Bool())
		case reflect.Float64:
			fv.SetFloat(fv.Float() + 0.125)
		case reflect.Slice:
			fv.Set(reflect.Append(fv, reflect.ValueOf("diff-sentinel")))
		default:
			t.Fatalf("cannot set kind %s", fv.Kind())
		}
		return
	}
	t.Fatalf("no field %q", parts[0])
}

func TestEveryClassifiedKeyIsDetectedByDiff(t *testing.T) {
	for key, kc := range keyClasses {
		t.Run(key, func(t *testing.T) {
			a, b := Defaults(), Defaults()
			setLeaf(t, reflect.ValueOf(b).Elem(), strings.Split(key, "."))
			hot, restart, noop := Diff(a, b)
			got := map[ReloadClass][]KeyChange{ClassHot: hot, ClassRestart: restart, ClassNoOp: noop}
			want := []KeyChange{{Key: key, Secret: kc.Secret}}
			for class, list := range got {
				exp := []KeyChange(nil)
				if class == kc.Class {
					exp = want
				}
				if !slices.Equal(list, exp) {
					t.Errorf("class %s: got %v, want %v", class, list, exp)
				}
			}
		})
	}
}

func keys(l []KeyChange) []string {
	var out []string
	for _, c := range l {
		out = append(out, c.Key)
	}
	return out
}

func TestDiff(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		hot     []string
		restart []string
		noop    []string
	}{
		{name: "identical", mutate: func(c *Config) {}},
		{name: "log level", mutate: func(c *Config) { c.Telemetry.Log.Level = "debug-x" }, hot: []string{"telemetry.log.level"}},
		{name: "smtp submissions port", mutate: func(c *Config) { c.SMTP.SubmissionsPort++ }, restart: []string{"smtp.submissions_port"}},
		{name: "storage min free", mutate: func(c *Config) { c.Storage.MinFreeMB++ }, restart: []string{"storage.min_free_mb"}},
		{name: "smtp port", mutate: func(c *Config) { c.SMTP.Port++ }, restart: []string{"smtp.port"}},
		{name: "bind addr", mutate: func(c *Config) { c.Server.BindAddr = "127.0.0.1" }, restart: []string{"server.bind_addr"}},
		{name: "control addr", mutate: func(c *Config) { c.Server.ControlAddr = "127.0.0.1:9081" }, restart: []string{"server.control_addr"}},
		{name: "datacenter", mutate: func(c *Config) { c.Server.Datacenter = "dc-x" }, noop: []string{"server.datacenter"}},
		{
			name: "spamassassin appears",
			mutate: func(c *Config) {
				c.Antispam.SpamAssassin = &SpamAssassinConfig{Enabled: true, Host: "x"}
			},
			restart: []string{"antispam.spamassassin.enabled", "antispam.spamassassin.host"},
		},
		{name: "queue nil vs empty", mutate: func(c *Config) { c.Queue = &QueueConfig{} }},
		{name: "trusted networks nil vs empty", mutate: func(c *Config) { c.SMTP.TrustedNetworks = []string{} }},
		{name: "secret", mutate: func(c *Config) { c.Web.SessionSecret = "other" }, restart: []string{"web.session_secret"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldCfg := &Config{SMTP: SMTPConfig{TrustedNetworks: nil}, Web: WebConfig{SessionSecret: "s"}}
			newCfg := *oldCfg
			tt.mutate(&newCfg)
			hot, restart, noop := Diff(oldCfg, &newCfg)
			if !slices.Equal(keys(hot), tt.hot) || !slices.Equal(keys(restart), tt.restart) || !slices.Equal(keys(noop), tt.noop) {
				t.Fatalf("got hot=%v restart=%v noop=%v", keys(hot), keys(restart), keys(noop))
			}
		})
	}
}

func TestDiffPointerAndRepeatedBlocks(t *testing.T) {
	base := func() *Config {
		return &Config{
			Pipeline: PipelineConfig{Stages: []StageConfig{
				{Name: "a", Enabled: true, Backend: "x"},
				{Name: "b", Enabled: true},
			}},
			Antispam: AntispamConfig{DNSBL: &DNSBLConfig{Enabled: true, Zones: []DNSBLZone{{Name: "z", Weight: 1}}}},
		}
	}
	tests := []struct {
		name    string
		mutate  func(c *Config)
		restart []string
	}{
		{"dnsbl disappears", func(c *Config) { c.Antispam.DNSBL = nil },
			[]string{"antispam.dnsbl.enabled", "antispam.dnsbl.zone.name", "antispam.dnsbl.zone.weight"}},
		{"stage flipped", func(c *Config) { c.Pipeline.Stages[1].Enabled = false }, []string{"pipeline.stage.enabled"}},
		{"stage appended", func(c *Config) {
			c.Pipeline.Stages = append(c.Pipeline.Stages, StageConfig{Name: "c", Enabled: true})
		}, []string{"pipeline.stage.enabled", "pipeline.stage.name"}},
		{"stage removed", func(c *Config) { c.Pipeline.Stages = c.Pipeline.Stages[:1] },
			[]string{"pipeline.stage.enabled", "pipeline.stage.name"}},
		{"zone weight", func(c *Config) { c.Antispam.DNSBL.Zones[0].Weight = 2 }, []string{"antispam.dnsbl.zone.weight"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := base(), base()
			tt.mutate(b)
			hot, restart, noop := Diff(a, b)
			if len(hot) != 0 || len(noop) != 0 || !slices.Equal(keys(restart), tt.restart) {
				t.Fatalf("got hot=%v restart=%v noop=%v, want restart %v", keys(hot), keys(restart), keys(noop), tt.restart)
			}
		})
	}
}

func TestDiffSecretFlag(t *testing.T) {
	a, b := &Config{}, &Config{}
	b.Web.SessionSecret = "x"
	b.SMTP.Port = 1
	_, restart, _ := Diff(a, b)
	want := []KeyChange{{"smtp.port", false}, {"web.session_secret", true}}
	if !slices.Equal(restart, want) {
		t.Fatalf("got %v, want %v", restart, want)
	}
}

// TestDiffRequiredSecretKeys hard-codes the set of keys that must be flagged
// Secret. secretKeys must not be its own oracle, and "username" matches none
// of the name patterns in TestSecretLookingKeysAreFlagged.
func TestDiffRequiredSecretKeys(t *testing.T) {
	tests := []struct {
		key    string
		mutate func(c *Config)
	}{
		{"web.session_secret", func(c *Config) { c.Web.SessionSecret = "x" }},
		{"cluster.gossip.encryption_key", func(c *Config) { c.Cluster.Gossip.EncryptionKey = "x" }},
		{"smtp.relay.password", func(c *Config) { c.SMTP.Relay.Password = "x" }},
		{"smtp.relay.username", func(c *Config) { c.SMTP.Relay.Username = "x" }},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			a, b := &Config{}, &Config{}
			tt.mutate(b)
			hot, restart, noop := Diff(a, b)
			all := slices.Concat(hot, restart, noop)
			want := []KeyChange{{Key: tt.key, Secret: true}}
			if !slices.Equal(all, want) {
				t.Fatalf("got %v, want %v", all, want)
			}
		})
	}
}

func TestDiffSeveralChangesSorted(t *testing.T) {
	a, b := &Config{}, &Config{}
	b.Telemetry.Log.Level = "x"
	b.Antispam.Builtin.Threshold = 1
	b.SMTP.TrustedNetworks = []string{"10.0.0.0/8"}
	b.Server.Datacenter = "x"
	b.Web.Enabled = true
	b.Storage.BlobGC = "x"
	b.IMAP.Port = 1
	hot, restart, noop := Diff(a, b)
	if got := keys(hot); !slices.Equal(got, []string{"antispam.builtin.threshold", "smtp.trusted_networks", "telemetry.log.level"}) {
		t.Errorf("hot = %v", got)
	}
	if got := keys(restart); !slices.Equal(got, []string{"imap.port", "storage.blob_gc"}) {
		t.Errorf("restart = %v", got)
	}
	if got := keys(noop); !slices.Equal(got, []string{"server.datacenter", "web.enabled"}) {
		t.Errorf("noop = %v", got)
	}
}

func TestDiffIncrementOneClasses(t *testing.T) {
	want := map[ReloadClass][]string{
		ClassHot: {
			"telemetry.log.level", "antispam.builtin.threshold", "antispam.builtin.high_threshold",
			"smtp.trusted_networks", "antivirus.clamav.on_failure",
		},
		ClassRestart: {
			"storage.blob_gc", "pipeline.quarantine_retention", "pipeline.error_retention",
			"storage.pebble.cache_size", "agent_server.tls.cert_file", "agent_server.tls.key_file",
			"agent_server.tls.ca_file",
		},
		ClassNoOp: {
			"telemetry.prometheus.enabled", "telemetry.prometheus.port", "antispam.builtin.enabled",
			"caldav.enabled", "web.enabled", "cluster.raft.data_dir",
			"server.datacenter", "smtp.tls.ca_file", "web.tls.ca_file", "storage.pebble.wal_dir",
		},
	}
	for class, ks := range want {
		for _, k := range ks {
			if got, ok := ClassOf(k); !ok || got != class {
				t.Errorf("%s: class %v (found %v), want %v", k, got, ok, class)
			}
		}
	}
	var hotCount, noopCount int
	for _, kc := range keyClasses {
		switch kc.Class {
		case ClassHot:
			hotCount++
		case ClassNoOp:
			noopCount++
		}
	}
	if hotCount != 5 || noopCount != 10 {
		t.Errorf("hot=%d noop=%d, want 5 and 10", hotCount, noopCount)
	}
}

func TestDiffDoesNotMutateInputs(t *testing.T) {
	a, b := Defaults(), Defaults()
	b.SMTP.TrustedNetworks = append(b.SMTP.TrustedNetworks, "10.0.0.0/8")
	b.Queue = nil
	ac, bc := Defaults(), Defaults()
	bc.SMTP.TrustedNetworks = append(bc.SMTP.TrustedNetworks, "10.0.0.0/8")
	bc.Queue = nil
	Diff(a, b)
	if !reflect.DeepEqual(a, ac) || !reflect.DeepEqual(b, bc) {
		t.Fatal("Diff mutated its inputs")
	}
}

func TestDiffNilConfig(t *testing.T) {
	for _, tc := range [][2]*Config{{nil, nil}, {nil, Defaults()}, {Defaults(), nil}} {
		t.Run(fmt.Sprint(tc[0] == nil, tc[1] == nil), func(t *testing.T) {
			Diff(tc[0], tc[1])
		})
	}
}

// TestEveryKeyHasExactlyOneClass guards against a key listed in two class
// lists: buildKeyClasses resolves that silently (the last list wins), so a key
// meant to be hot could quietly become restart.
func TestEveryKeyHasExactlyOneClass(t *testing.T) {
	seen := map[string]string{}
	for name, keys := range map[string][]string{"hot": hotKeys, "noop": noopKeys, "restart": restartKeys} {
		for _, k := range keys {
			if prev, ok := seen[k]; ok {
				t.Errorf("key %q is listed as both %s and %s", k, prev, name)
			}
			seen[k] = name
		}
	}
}
