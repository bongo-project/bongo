package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

const (
	totpIssuer        = "Bongo"
	recoveryCodeChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	recoveryCodeLen   = 8 // each segment length
)

// GenerateTOTP generates a new TOTP key for the given email.
// Uses issuer "Bongo" and the email as the account name.
func GenerateTOTP(email string) (*otp.Key, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: email,
	})
	if err != nil {
		return nil, fmt.Errorf("generate totp key: %w", err)
	}
	return key, nil
}

// ValidateTOTP validates a 6-digit TOTP code against the secret.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

// GenerateRecoveryCodes generates recovery codes.
// Returns plaintext codes (for display) and bcrypt hashes (for storage).
// Each code has the format XXXX-XXXX using alphanumeric characters.
func GenerateRecoveryCodes(count int) ([]string, []string, error) {
	plaintext := make([]string, count)
	hashed := make([]string, count)

	for i := 0; i < count; i++ {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, nil, fmt.Errorf("generate recovery code %d: %w", i, err)
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(code), PasswordHashCost)
		if err != nil {
			return nil, nil, fmt.Errorf("hash recovery code %d: %w", i, err)
		}

		plaintext[i] = code
		hashed[i] = string(hash)
	}

	return plaintext, hashed, nil
}

// ValidateRecoveryCode checks a plaintext code against stored bcrypt hashes.
// Returns whether it matched and the index of the match (for removal).
// Returns (false, -1) when no match is found.
func ValidateRecoveryCode(hashedCodes []string, code string) (bool, int) {
	for i, h := range hashedCodes {
		if err := bcrypt.CompareHashAndPassword([]byte(h), []byte(code)); err == nil {
			return true, i
		}
	}
	return false, -1
}

// generateRecoveryCode produces a single random code in XXXX-XXXX format.
func generateRecoveryCode() (string, error) {
	charCount := big.NewInt(int64(len(recoveryCodeChars)))
	buf := make([]byte, recoveryCodeLen*2)

	for i := range buf {
		n, err := rand.Int(rand.Reader, charCount)
		if err != nil {
			return "", fmt.Errorf("rand int: %w", err)
		}
		buf[i] = recoveryCodeChars[n.Int64()]
	}

	return string(buf[:recoveryCodeLen]) + "-" + string(buf[recoveryCodeLen:]), nil
}
