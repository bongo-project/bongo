package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func TestGenerateTOTP(t *testing.T) {
	email := "alice@example.com"

	key, err := GenerateTOTP(email)
	if err != nil {
		t.Fatalf("GenerateTOTP(%q) returned error: %v", email, err)
	}
	if key == nil {
		t.Fatal("GenerateTOTP returned nil key")
	}

	if key.Issuer() != totpIssuer {
		t.Errorf("issuer = %q, want %q", key.Issuer(), totpIssuer)
	}
	if key.AccountName() != email {
		t.Errorf("account name = %q, want %q", key.AccountName(), email)
	}
	if key.Secret() == "" {
		t.Error("secret is empty")
	}
}

func TestValidateTOTP(t *testing.T) {
	email := "bob@example.com"

	key, err := GenerateTOTP(email)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}

	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}

	tests := []struct {
		name  string
		code  string
		valid bool
	}{
		{
			name:  "valid code",
			code:  code,
			valid: true,
		},
		{
			name:  "wrong code",
			code:  "000000",
			valid: false,
		},
		{
			name:  "empty code",
			code:  "",
			valid: false,
		},
		{
			name:  "too short",
			code:  "12345",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateTOTP(key.Secret(), tt.code)
			if got != tt.valid {
				t.Errorf("ValidateTOTP(secret, %q) = %v, want %v", tt.code, got, tt.valid)
			}
		})
	}
}

func TestGenerateRecoveryCodes(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "standard 8 codes", count: 8},
		{name: "single code", count: 1},
		{name: "ten codes", count: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plaintext, hashed, err := GenerateRecoveryCodes(tt.count)
			if err != nil {
				t.Fatalf("GenerateRecoveryCodes(%d): %v", tt.count, err)
			}

			if len(plaintext) != tt.count {
				t.Errorf("len(plaintext) = %d, want %d", len(plaintext), tt.count)
			}
			if len(hashed) != tt.count {
				t.Errorf("len(hashed) = %d, want %d", len(hashed), tt.count)
			}

			// Check format: XXXX-XXXX
			for i, code := range plaintext {
				parts := strings.Split(code, "-")
				if len(parts) != 2 {
					t.Errorf("plaintext[%d] = %q: expected XXXX-XXXX format", i, code)
					continue
				}
				if len(parts[0]) != recoveryCodeLen || len(parts[1]) != recoveryCodeLen {
					t.Errorf("plaintext[%d] = %q: segment lengths %d/%d, want %d/%d",
						i, code, len(parts[0]), len(parts[1]), recoveryCodeLen, recoveryCodeLen)
				}
			}

			// Check uniqueness
			seen := make(map[string]bool, tt.count)
			for _, code := range plaintext {
				if seen[code] {
					t.Errorf("duplicate code: %q", code)
				}
				seen[code] = true
			}

			// Check that hashes verify against their plaintext counterparts
			for i, hash := range hashed {
				if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext[i])); err != nil {
					t.Errorf("hash[%d] does not verify against plaintext[%d]: %v", i, i, err)
				}
			}
		})
	}
}

func TestValidateRecoveryCode(t *testing.T) {
	count := 5
	plaintext, hashed, err := GenerateRecoveryCodes(count)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	tests := []struct {
		name      string
		code      string
		wantMatch bool
		wantIdx   int
	}{
		{
			name:      "first code matches",
			code:      plaintext[0],
			wantMatch: true,
			wantIdx:   0,
		},
		{
			name:      "last code matches",
			code:      plaintext[count-1],
			wantMatch: true,
			wantIdx:   count - 1,
		},
		{
			name:      "middle code matches",
			code:      plaintext[2],
			wantMatch: true,
			wantIdx:   2,
		},
		{
			name:      "wrong code rejected",
			code:      "AAAA-BBBB",
			wantMatch: false,
			wantIdx:   -1,
		},
		{
			name:      "empty code rejected",
			code:      "",
			wantMatch: false,
			wantIdx:   -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, idx := ValidateRecoveryCode(hashed, tt.code)
			if matched != tt.wantMatch {
				t.Errorf("matched = %v, want %v", matched, tt.wantMatch)
			}
			if idx != tt.wantIdx {
				t.Errorf("idx = %d, want %d", idx, tt.wantIdx)
			}
		})
	}
}

func TestValidateTOTPStep(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	now := time.Unix(1_700_000_010, 0)
	cur := now.Unix() / 30

	codeAt := func(step int64) string {
		c, err := totp.GenerateCode(secret, time.Unix(step*30, 0))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	tests := []struct {
		name     string
		code     string
		last     int64
		wantStep int64
		wantOK   bool
	}{
		{"current step, fresh", codeAt(cur), 0, cur, true},
		{"step before, fresh", codeAt(cur - 1), 0, cur - 1, true},
		{"step after, fresh", codeAt(cur + 1), 0, cur + 1, true},
		{"current step, already used", codeAt(cur), cur, 0, false},
		{"older step than last used", codeAt(cur - 1), cur, 0, false},
		{"next step after last used", codeAt(cur + 1), cur, cur + 1, true},
		{"outside skew", codeAt(cur + 2), 0, 0, false},
		{"wrong code", "000000", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, ok := ValidateTOTPStep(secret, tt.code, now, tt.last)
			if ok != tt.wantOK || (ok && step != tt.wantStep) {
				t.Errorf("got (%d, %v), want (%d, %v)", step, ok, tt.wantStep, tt.wantOK)
			}
		})
	}
}
