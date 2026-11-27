package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := buildVersion
	buildVersion = v
	t.Cleanup(func() { buildVersion = old })
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name    string
		version string
		args    []string
		env     map[string]string
		wantErr string
		want    settings
	}{
		{
			name: "defaults from the release build", version: "v0.17.0",
			want: settings{listen: ":8080", cfg: config{Latest: "0.17.0", ReleaseNotesURL: "https://update.bongo-project.org/releases/0.17.0"}},
		},
		{name: "dev build refuses", version: "dev", wantErr: "latest is not set"},
		{name: "describe build refuses", version: "v0.17.0-3-gabcdef", wantErr: "latest is not set"},
		{name: "pre-release build refuses", version: "v0.17.0-rc.1", wantErr: "latest is not set"},
		{
			name: "pre-release build with explicit latest", version: "v0.17.0-rc.1",
			args: []string{"--latest", "0.16.2"},
			want: settings{listen: ":8080", cfg: config{Latest: "0.16.2", ReleaseNotesURL: "https://update.bongo-project.org/releases/0.16.2"}},
		},
		{name: "double v latest names latest", version: "dev", args: []string{"--latest", "vv0.17.0"}, wantErr: "latest is not a semantic version"},
		{
			name: "env used when no flag", version: "dev",
			env: map[string]string{
				"BONGO_UPDATE_LATEST": "v1.2.3", "BONGO_UPDATE_LISTEN": "127.0.0.1:9", "BONGO_UPDATE_SECURITY": "true",
				"BONGO_UPDATE_RELEASE_NOTES_URL": "https://n.example/x",
			},
			want: settings{listen: "127.0.0.1:9", cfg: config{Latest: "1.2.3", ReleaseNotesURL: "https://n.example/x", Security: true}},
		},
		{
			name: "flag beats env", version: "dev",
			args: []string{"--latest", "2.0.0", "--security=false", "--listen", ":1"},
			env:  map[string]string{"BONGO_UPDATE_LATEST": "1.0.0", "BONGO_UPDATE_SECURITY": "true", "BONGO_UPDATE_LISTEN": ":2"},
			want: settings{listen: ":1", cfg: config{Latest: "2.0.0", ReleaseNotesURL: "https://update.bongo-project.org/releases/2.0.0"}},
		},
		{name: "security 1 refused", version: "v1.0.0", args: []string{"--security", "1"}, wantErr: "security"},
		{name: "security env garbage refused", version: "v1.0.0", env: map[string]string{"BONGO_UPDATE_SECURITY": "yes"}, wantErr: "security"},
		{name: "bad latest", version: "v1.0.0", args: []string{"--latest", "1.0"}, wantErr: "semantic version"},
		{name: "http notes url", version: "v1.0.0", args: []string{"--release-notes-url", "http://x.example/"}, wantErr: "https"},
		{name: "unknown flag", version: "v1.0.0", args: []string{"--bogus"}, wantErr: "bogus"},
		{name: "dsn is not a flag", version: "v1.0.0", args: []string{"--dsn", "postgres://x"}, wantErr: "dsn"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withVersion(t, tc.version)
			var stderr bytes.Buffer
			got, err := resolve(tc.args, env(tc.env), &stderr)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error()+stderr.String(), tc.wantErr) {
					t.Fatalf("err %v (stderr %q), want %q", err, stderr.String(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNoDSNFlag(t *testing.T) {
	var stderr bytes.Buffer
	_, err := resolve([]string{"-h"}, env(nil), &stderr)
	if err == nil {
		t.Fatal("expected help error")
	}
	if strings.Contains(strings.ToLower(stderr.String()), "dsn") {
		t.Errorf("usage mentions a DSN flag: %s", stderr.String())
	}
}

func TestRunRefusesWithoutDSN(t *testing.T) {
	withVersion(t, "v1.0.0")
	var stderr bytes.Buffer
	err := run(context.Background(), nil, env(nil), &stderr)
	if err == nil || !strings.Contains(err.Error(), "BONGO_UPDATE_DSN") {
		t.Errorf("err %v", err)
	}
}

func TestRunNeverEchoesDSN(t *testing.T) {
	withVersion(t, "v1.0.0")
	oldE, oldT := schemaRetryEvery, schemaRetryTotal
	schemaRetryEvery, schemaRetryTotal = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { schemaRetryEvery, schemaRetryTotal = oldE, oldT })

	dsns := []string{
		"postgres://sentineluser:sentinelpass@sentinel-host.invalid:1/sentineldb",
		"postgres://sentineluser:sentinelpass@sentinel-host.invalid:notaport/sentineldb",
		"host=sentinel-host.invalid user=sentineluser password=sentinelpass dbname=sentineldb sslmode=bogus",
	}
	for _, dsn := range dsns {
		var stderr bytes.Buffer
		err := run(context.Background(), nil, env(map[string]string{"BONGO_UPDATE_DSN": dsn}), &stderr)
		if err == nil {
			t.Fatalf("run succeeded for %q", dsn)
		}
		if strings.Contains(strings.ToLower(err.Error()+stderr.String()), "sentinel") {
			t.Errorf("DSN leaked: err %q stderr %q", err, stderr.String())
		}
	}
}
