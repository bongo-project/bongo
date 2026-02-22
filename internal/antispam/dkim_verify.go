package antispam

import (
	"bytes"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// VerifyDKIM checks the DKIM signature of a raw email message.
// Returns the verification result ("pass", "fail", "none") and the signing domain.
func VerifyDKIM(rawMessage []byte) (result string, domain string, err error) {
	verifications, err := dkim.Verify(bytes.NewReader(rawMessage))
	if err != nil {
		return "none", "", nil
	}

	if len(verifications) == 0 {
		return "none", "", nil
	}

	// Check the first verification result.
	v := verifications[0]
	sigDomain := v.Domain

	if v.Err != nil {
		return "fail", sigDomain, nil
	}

	return "pass", sigDomain, nil
}

// ExtractDKIMDomain extracts the d= domain from a DKIM-Signature header value.
func ExtractDKIMDomain(headerValue string) string {
	for _, part := range strings.Split(headerValue, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "d=") {
			return strings.TrimSpace(part[2:])
		}
	}
	return ""
}
