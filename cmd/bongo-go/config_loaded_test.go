package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/bongo-project/bongo/internal/config"
)

func TestLogConfigLoadedCarriesFileStamp(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	// Built in a non-UTC zone so dropping the UTC normalisation is caught
	// whatever zone the test host runs in.
	mt := time.Date(2026, 3, 4, 7, 6, 7, 0, time.FixedZone("x", 2*3600))

	logConfigLoaded(logger, "/etc/bongo/bongo.hcl", config.FileStamp{ModTime: mt, Size: 1234}, config.Defaults())

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("bad log line %q: %v", buf.String(), err)
	}
	if rec["msg"] != "config loaded" {
		t.Errorf("msg = %v", rec["msg"])
	}
	if rec["file_mtime"] != "2026-03-04T05:06:07Z" {
		t.Errorf("file_mtime = %v", rec["file_mtime"])
	}
	if rec["file_size"] != float64(1234) {
		t.Errorf("file_size = %v", rec["file_size"])
	}
}
