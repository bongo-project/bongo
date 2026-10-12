package config

import (
	"log/slog"
	"reflect"
	"testing"
)

// fill populates every field so the clone has pointers and slices to share.
func fill(v reflect.Value, n *int) {
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			fill(v.Field(i), n)
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), n)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			fill(s.Index(i), n)
		}
		v.Set(s)
	case reflect.String:
		*n++
		v.SetString("s" + string(rune('a'+*n%26)))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		*n++
		v.SetInt(int64(*n))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(0.5)
	}
}

// shared reports the path of any pointer or slice backing array that both
// values reference.
func shared(path string, a, b reflect.Value) string {
	switch a.Kind() {
	case reflect.Struct:
		for i := range a.NumField() {
			if p := shared(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i)); p != "" {
				return p
			}
		}
	case reflect.Pointer:
		if a.IsNil() {
			return ""
		}
		if a.Pointer() == b.Pointer() {
			return path
		}
		return shared(path, a.Elem(), b.Elem())
	case reflect.Slice:
		if a.Len() == 0 {
			return ""
		}
		if a.Pointer() == b.Pointer() {
			return path
		}
		for i := range a.Len() {
			if p := shared(path, a.Index(i), b.Index(i)); p != "" {
				return p
			}
		}
	}
	return ""
}

func TestClone(t *testing.T) {
	t.Run("deep copy shares no storage", func(t *testing.T) {
		orig := &Config{}
		n := 0
		fill(reflect.ValueOf(orig).Elem(), &n)
		got := Clone(orig)
		if !reflect.DeepEqual(orig, got) {
			t.Fatal("clone differs from original")
		}
		if p := shared("Config", reflect.ValueOf(orig).Elem(), reflect.ValueOf(got).Elem()); p != "" {
			t.Fatalf("clone shares storage at %s", p)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		orig := Defaults()
		got := Clone(orig)
		if !reflect.DeepEqual(orig, got) {
			t.Fatal("clone of defaults differs")
		}
		got.SMTP.TrustedNetworks = append(got.SMTP.TrustedNetworks, "10.0.0.0/8")
		got.Pipeline.Stages[0].Name = "changed"
		if orig.Pipeline.Stages[0].Name == "changed" || len(orig.SMTP.TrustedNetworks) != 0 {
			t.Fatal("mutating clone changed original")
		}
	})
	t.Run("nil", func(t *testing.T) {
		if Clone(nil) != nil {
			t.Fatal("Clone(nil) != nil")
		}
	})
}

func TestLogConfigSlogLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn,
		"error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo,
	} {
		if got := (LogConfig{Level: in}).SlogLevel(); got != want {
			t.Errorf("SlogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}
