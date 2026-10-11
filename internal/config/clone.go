package config

import (
	"log/slog"
	"reflect"
)

// Clone returns a deep copy of c that shares no pointer or slice storage with
// it. The reloader uses it to keep a baseline and to build a candidate config
// without ever mutating the running *Config. A nil config clones to nil.
func Clone(c *Config) *Config {
	if c == nil {
		return nil
	}
	out := new(Config)
	deepCopy(reflect.ValueOf(out).Elem(), reflect.ValueOf(c).Elem())
	return out
}

// deepCopy copies src into dst (both the same type, dst settable). Config
// holds structs, pointers, slices and scalars only.
func deepCopy(dst, src reflect.Value) {
	switch src.Kind() {
	case reflect.Struct:
		for i := range src.NumField() {
			deepCopy(dst.Field(i), src.Field(i))
		}
	case reflect.Ptr:
		if src.IsNil() {
			return
		}
		p := reflect.New(src.Type().Elem())
		deepCopy(p.Elem(), src.Elem())
		dst.Set(p)
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		s := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
		for i := range src.Len() {
			deepCopy(s.Index(i), src.Index(i))
		}
		dst.Set(s)
	default:
		dst.Set(src)
	}
}

// SlogLevel maps the configured level name to a slog level. Unknown or empty
// values mean info, which is what startup has always done.
func (l LogConfig) SlogLevel() slog.Level {
	switch l.Level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
