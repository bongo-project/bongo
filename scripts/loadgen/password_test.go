package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestNewPassword(t *testing.T) {
	a, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if a.Reveal() == b.Reveal() {
		t.Error("two passwords are equal")
	}
	if n := len(a.Reveal()); n < 8 || n > 72 {
		t.Errorf("length %d outside 8-72", n)
	}
}

func TestPasswordNeverPrints(t *testing.T) {
	p, err := NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	secret := p.Reveal()
	wrapper := struct {
		P  Password
		PP *Password
	}{p, &p}

	var outputs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		outputs = append(outputs, fmt.Sprintf(verb, p), fmt.Sprintf(verb, &p), fmt.Sprintf(verb, wrapper))
	}
	j, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, string(j))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "pw", p, slog.Group("g", "pw", p))
	slog.New(slog.NewTextHandler(&buf, nil)).Info("x", "pw", p)
	outputs = append(outputs, buf.String())

	for i, o := range outputs {
		if strings.Contains(o, secret) || strings.Contains(o, secret[:8]) {
			t.Errorf("output %d leaks the password: %q", i, o)
		}
	}
	if got := fmt.Sprint(p); got != redacted {
		t.Errorf("Sprint = %q, want %q", got, redacted)
	}
}
