package handlers

import (
	"testing"

	"github.com/bongo-go/bongo-go/internal/web/templates"
)

func TestComposeAdminUserEmail(t *testing.T) {
	domains := []templates.DomainInfo{
		{Name: "example.test", Enabled: true},
		{Name: "disabled.test", Enabled: false},
	}

	tests := []struct {
		name     string
		email    string
		username string
		domain   string
		want     string
		wantErr  bool
	}{
		{
			name:    "legacy full email is accepted",
			email:   "alice@example.test",
			want:    "alice@example.test",
			wantErr: false,
		},
		{
			name:     "username and enabled domain compose full address",
			username: "alice",
			domain:   "example.test",
			want:     "alice@example.test",
			wantErr:  false,
		},
		{
			name:     "domain matching is case insensitive",
			username: "alice",
			domain:   "EXAMPLE.TEST",
			want:     "alice@example.test",
			wantErr:  false,
		},
		{
			name:     "username must not include domain",
			username: "alice@example.test",
			domain:   "example.test",
			wantErr:  true,
		},
		{
			name:     "disabled domain is rejected",
			username: "alice",
			domain:   "disabled.test",
			wantErr:  true,
		},
		{
			name:     "unknown domain is rejected",
			username: "alice",
			domain:   "unknown.test",
			wantErr:  true,
		},
		{
			name:    "invalid legacy email is rejected",
			email:   "not an address",
			wantErr: true,
		},
		{
			name:    "missing address fields are rejected",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := composeAdminUserEmail(tt.email, tt.username, tt.domain, domains)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("composeAdminUserEmail() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("composeAdminUserEmail() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("composeAdminUserEmail() = %q, want %q", got, tt.want)
			}
		})
	}
}
