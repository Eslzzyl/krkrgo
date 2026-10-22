package version

import (
	_ "embed"
	"strings"
)

// The same file is also consumed by the Android, Apple, and packaging builds.
//
//go:embed version.xcconfig
var config string

var (
	Version = configValue("KRKRGO_VERSION", "dev")
	Build   = configValue("KRKRGO_BUILD", "0")
)

func configValue(key, fallback string) string {
	for _, line := range strings.Split(config, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != key {
			continue
		}
		value := strings.TrimSpace(parts[1])
		if value != "" {
			return value
		}
	}
	return fallback
}
