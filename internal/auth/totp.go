package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

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

// totpPeriod is the RFC 6238 time-step length in seconds.
const totpPeriod = 30

// ValidateTOTPStep validates a 6-digit TOTP code against the secret within one
// step either side of now, and accepts it only if its time-step is later than
// lastStep, the step of the last code the account consumed. It returns the
// matched step so the caller can record it; a code accepted once is therefore
// refused for the rest of its validity window.
func ValidateTOTPStep(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	cur := now.Unix() / totpPeriod
	for step := cur - 1; step <= cur+1; step++ {
		if step <= lastStep {
			continue
		}
		ok, err := totp.ValidateCustom(code, secret, time.Unix(step*totpPeriod, 0), totp.ValidateOpts{
			Period:    totpPeriod,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && ok {
			return step, true
		}
	}
	return 0, false
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
