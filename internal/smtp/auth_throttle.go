package smtp

import "time"

// SMTP AUTH brute-force tuning. The per-IP failure counter itself lives in the
// shared internal/authlimit package (Backend.authFail); these constants seed it
// and also bound the per-connection counter (Session.authFailures).
const (
	// maxAuthFailures is the number of failed AUTH attempts (per connection and
	// per source IP) tolerated before further attempts are throttled.
	maxAuthFailures = 5

	// authFailWindow is how long a source IP stays locked out after exceeding
	// maxAuthFailures, and the idle period after which its failure count is
	// forgotten.
	authFailWindow = 15 * time.Minute
)
