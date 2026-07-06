package antispam

import (
	"bytes"
	"strings"

	"github.com/emersion/go-msgauth/dkim"
)

// maxDKIMVerifications caps how many DKIM-Signature headers are actually
// verified. Without a cap, go-msgauth spawns one goroutine + one DNS TXT lookup
// + one crypto verification per signature header, so a single message crafted
// with tens of thousands of DKIM-Signature headers (well within the 25 MB
// message cap) becomes a resource-exhaustion DoS against the public MX. RFC 6376
// deployments realistically carry only a handful of signatures.
const maxDKIMVerifications = 5

// VerifyDKIM checks the DKIM signature of a raw email message.
// Returns the verification result ("pass", "fail", "none") and the signing domain.
func VerifyDKIM(rawMessage []byte) (result string, domain string, err error) {
	verifications, err := dkim.VerifyWithOptions(bytes.NewReader(rawMessage), &dkim.VerifyOptions{
		MaxVerifications: maxDKIMVerifications,
	})
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
