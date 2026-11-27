package main

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/bongo-project/bongo/internal/updatecheck"
)

// maxBody is the largest request body the service reads.
const maxBody = 1024

// checkRequest is a validated /v1/check body.
type checkRequest struct {
	version string
	id      string
}

// parseCheck validates a request body. The body must be a JSON object that
// holds exactly the two string fields "version" and "instance_id". It
// decodes into a map of raw values on purpose: struct decoding matches keys
// case-insensitively and turns null into an empty string, and the contract
// accepts neither.
func parseCheck(body []byte) (checkRequest, bool) {
	// utf8.Valid matches the Worker's fatal decoder. The key, semver and hex
	// checks already refuse invalid UTF-8, so no test can isolate it; keep it.
	if len(body) > maxBody || !utf8.Valid(body) {
		return checkRequest{}, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || len(obj) != 2 {
		return checkRequest{}, false
	}
	version, ok := rawString(obj, "version")
	if !ok {
		return checkRequest{}, false
	}
	id, ok := rawString(obj, "instance_id")
	if !ok {
		return checkRequest{}, false
	}
	version, ok = normaliseVersion(version)
	if !ok {
		return checkRequest{}, false
	}
	id = strings.ToLower(id)
	if !updatecheck.ValidInstanceID(id) {
		return checkRequest{}, false
	}
	return checkRequest{version: version, id: id}, true
}

// rawString returns the value under key when it is a JSON string.
func rawString(obj map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := obj[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// normaliseVersion removes one leading "v" and checks strict semantic
// versioning without build metadata, at most 64 characters.
func normaliseVersion(v string) (string, bool) {
	v = strings.TrimPrefix(v, "v")
	if _, ok := updatecheck.ParseSemver(v); !ok {
		return "", false
	}
	return v, true
}
