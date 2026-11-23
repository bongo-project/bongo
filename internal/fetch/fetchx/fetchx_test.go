package fetchx

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLimitReader(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		max     int64
		wantErr error
		wantLen int
	}{
		{"fits", "hello", 10, nil, 5},
		{"exact", "hello", 5, nil, 5},
		{"one over", "hello!", 5, ErrTooLarge, 5},
		{"far over", strings.Repeat("x", 1000), 10, ErrTooLarge, 10},
		{"empty", "", 5, nil, 0},
		{"zero max, empty", "", 0, nil, 0},
		{"zero max, data", "a", 0, ErrTooLarge, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := io.ReadAll(LimitReader(strings.NewReader(tt.in), tt.max))
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(b) != tt.wantLen {
				t.Fatalf("read %d bytes, want %d", len(b), tt.wantLen)
			}
		})
	}
}
