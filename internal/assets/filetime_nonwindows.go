//go:build !windows

package assets

import "os"

// fileTimes falls back to the modification time where the portable
// os.FileInfo does not expose access or creation times.
func fileTimes(info os.FileInfo, modUnix int64) (int64, int64) {
	return modUnix, modUnix
}
