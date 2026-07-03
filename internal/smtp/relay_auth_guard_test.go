package smtp

import (
	"testing"
)

func TestShouldSendAuth(t *testing.T) {
	tests := []struct {
		name          string
		tlsCompleted  bool
		hasCreds      bool
		allowInsecure bool
		wantSend      bool
		wantErr       bool
	}{
		{
			name:          "no credentials configured — never send auth",
			tlsCompleted:  false,
			hasCreds:      false,
			allowInsecure: false,
			wantSend:      false,
			wantErr:       false,
		},
		{
			name:          "no credentials with TLS — still no auth",
			tlsCompleted:  true,
			hasCreds:      false,
			allowInsecure: false,
			wantSend:      false,
			wantErr:       false,
		},
		{
			name:          "credentials + STARTTLS completed — auth proceeds",
			tlsCompleted:  true,
			hasCreds:      true,
			allowInsecure: false,
			wantSend:      true,
			wantErr:       false,
		},
		{
			name:          "credentials + no TLS + allow_insecure_auth=false — refused",
			tlsCompleted:  false,
			hasCreds:      true,
			allowInsecure: false,
			wantSend:      false,
			wantErr:       true,
		},
		{
			name:          "credentials + no TLS + allow_insecure_auth=true — proceeds",
			tlsCompleted:  false,
			hasCreds:      true,
			allowInsecure: true,
			wantSend:      true,
			wantErr:       false,
		},
		{
			name:          "credentials + TLS + allow_insecure_auth=true — proceeds (TLS wins)",
			tlsCompleted:  true,
			hasCreds:      true,
			allowInsecure: true,
			wantSend:      true,
			wantErr:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			send, err := shouldSendAuth(tc.tlsCompleted, tc.hasCreds, tc.allowInsecure)
			if tc.wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if send != tc.wantSend {
				t.Errorf("send = %v, want %v", send, tc.wantSend)
			}
		})
	}
}
