//go:build !windows

package runtime

import "github.com/eslzzyl/krkrgo/internal/tjs"

// The system registry is a Windows-only facility. On other platforms every
// registry read is unsuccessful, matching System.readRegValue's void result.
func readSystemRegistryValue(string) (tjs.Value, bool) {
	return tjs.VoidValue, false
}
