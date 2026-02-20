package version

import "fmt"

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	return fmt.Sprintf("bongo-go %s (commit: %s, built: %s)", Version, Commit, Date)
}
