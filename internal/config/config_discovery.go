package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// configSearchPaths is the ordered list of candidate paths checked when no
// explicit flag or env var is provided.
var configSearchPaths = []string{
	"bongo.hcl",
	"/etc/bongo/bongo.hcl",
}

// envVarConfig is the environment variable name used as an alternative to the
// -c/--config flag.
const envVarConfig = "BONGO_CONFIG"

// DiscoverConfigFile returns the path of the HCL config file to load,
// following this priority order:
//
//  1. flagPath — explicit -c/--config flag value (error if set but not found)
//  2. BONGO_CONFIG environment variable (silently skipped if not found)
//  3. ./bongo.hcl in the current working directory
//  4. /etc/bongo/bongo.hcl system default
//  5. No file found — returns ("", nil); caller should use defaults
func DiscoverConfigFile(flagPath string) (string, error) {
	// 1. Explicit flag takes highest priority.
	if flagPath != "" {
		abs, err := filepath.Abs(flagPath)
		if err != nil {
			return "", fmt.Errorf("resolving config path %q: %w", flagPath, err)
		}
		if _, err := os.Stat(abs); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("config file not found: %s", abs)
			}
			return "", fmt.Errorf("checking config file %s: %w", abs, err)
		}
		return abs, nil
	}

	// 2. BONGO_CONFIG environment variable.
	if envPath := os.Getenv(envVarConfig); envPath != "" {
		abs, err := filepath.Abs(envPath)
		if err != nil {
			return "", fmt.Errorf("resolving BONGO_CONFIG path %q: %w", envPath, err)
		}
		// #nosec G703 -- BONGO_CONFIG is operator-supplied, not attacker input.
		// Setting it means already controlling the server process environment,
		// at which point the config path is the least of what you can change.
		// This only stats the path to decide whether to use it; the file is
		// parsed as HCL, never executed. Same trust model as the --config flag
		// above and as internal/importer/state.go.
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
		// Silently fall through if env-specified file is not found.
	}

	// 3 & 4. Walk the ordered search paths.
	for _, candidate := range configSearchPaths {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			return abs, nil
		}
	}

	// No config file found anywhere — caller uses defaults.
	return "", nil
}
