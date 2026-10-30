package version

import "fmt"

const Name = "bongo"

// Set via ldflags at build time.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// BuildInfo is the version metadata embedded into the binary at build time.
type BuildInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Info returns the current build metadata.
func Info() BuildInfo {
	return BuildInfo{
		Name:    Name,
		Version: Version,
		Commit:  Commit,
		Date:    Date,
	}
}

func String() string {
	return fmt.Sprintf("%s %s (commit: %s, built: %s)", Name, Version, Commit, Date)
}
