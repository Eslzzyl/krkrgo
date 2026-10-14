//go:build windows

package assets

import (
	"os"
	"syscall"
)

// fileTimes reports the access and creation times Windows records for a file,
// falling back to the modification time when the file system omits them. The
// reference implementation can only supply the modification time, so the
// fallback keeps the same contract on file systems without these values.
func fileTimes(info os.FileInfo, modUnix int64) (int64, int64) {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return modUnix, modUnix
	}
	return filetimeUnix(data.LastAccessTime, modUnix), filetimeUnix(data.CreationTime, modUnix)
}

// filetimeUnix converts a Windows FILETIME (100-nanosecond ticks since
// 1601-01-01) to Unix seconds, returning fallback for an empty timestamp.
func filetimeUnix(value syscall.Filetime, fallback int64) int64 {
	// Constant offset between the FILETIME and Unix epochs, in seconds.
	const filetimeEpochOffset = 11644473600
	ticks := uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
	if ticks == 0 {
		return fallback
	}
	return int64(ticks/10_000_000) - filetimeEpochOffset
}
