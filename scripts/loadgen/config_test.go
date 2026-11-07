package main

import (
	"reflect"
	"testing"
)

func TestListFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantAllow []string
		wantErr   bool
	}{
		{"repeat", []string{"--target-allow", "a.test.invalid", "--target-allow", "b.test.invalid:25"}, []string{"a.test.invalid", "b.test.invalid:25"}, false},
		{"comma and spaces", []string{"--target-allow", " a.test.invalid , B.test.invalid:25 "}, []string{"a.test.invalid", "b.test.invalid:25"}, false},
		{"trailing comma", []string{"--target-allow", "a.test.invalid,"}, nil, true},
		{"bracketed v6", []string{"--target-allow", "[::1]:25,[::2]"}, []string{"[::1]:25", "::2"}, false},
		{"none", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseArgs(t, tt.args...)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			var got []string
			for _, a := range cfg.Allow {
				got = append(got, a.String())
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.wantAllow) {
				t.Errorf("allow = %v, want %v", got, tt.wantAllow)
			}
		})
	}
}

func TestParseTargetCanonical(t *testing.T) {
	tests := []struct{ in, host string }{
		{"::1", "::1"},
		{"0:0::1", "::1"},
		{"[::ffff:10.0.0.1]:25", "10.0.0.1"},
		{"Host.Test.Invalid.", "host.test.invalid"},
	}
	for _, tt := range tests {
		got, err := parseTarget(tt.in)
		if err != nil || got.Host != tt.host {
			t.Errorf("parseTarget(%q) = %+v, %v; want host %q", tt.in, got, err, tt.host)
		}
	}
}
