package runtime

import (
	"errors"
	"log"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/media/ffmpeg"
)

var sidecarReportOnce sync.Once

// sidecarUnavailable reports whether the FFmpeg dynamic libraries are missing
// or ABI-incompatible, which is a deployment condition rather than a script
// error.
func sidecarUnavailable(err error) bool {
	return errors.Is(err, ffmpeg.ErrUnavailable) || errors.Is(err, ffmpeg.ErrABIMismatch)
}

// reportSidecarUnavailable records the first sidecar load failure once per
// process. Callers degrade to a no-op playback path afterward so a missing
// deployment dependency cannot terminate the runtime.
func reportSidecarUnavailable(err error) {
	sidecarReportOnce.Do(func() {
		log.Printf("krkrgo: FFmpeg shared libraries unavailable, FFmpeg-backed media is skipped: %v", err)
	})
}
