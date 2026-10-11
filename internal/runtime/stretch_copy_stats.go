package runtime

import (
	"log"
	"os"
	"sync"
	"time"
)

// Read once at startup so disabled stretchCopy calls only pay a predictable branch.
var stretchCopyStatsEnabled = os.Getenv("KRKRGO_RENDER_STATS") != ""

var stretchCopyStats struct {
	sync.Mutex
	started                   time.Time
	calls                     uint64
	totalTime, maxTime        time.Duration
	sourcePixels, sourceBytes uint64
	outputPixels, outputBytes uint64
}

func recordStretchCopyStats(elapsed time.Duration, sourceBytes int, width, height int32) {
	outPixels := uint64(0)
	if width > 0 && height > 0 {
		outPixels = uint64(width) * uint64(height)
	}
	now := time.Now()
	stretchCopyStats.Lock()
	d := &stretchCopyStats
	if d.started.IsZero() {
		d.started = now
	}
	d.calls++
	d.totalTime += elapsed
	if elapsed > d.maxTime {
		d.maxTime = elapsed
	}
	d.sourceBytes += uint64(sourceBytes)
	d.sourcePixels += uint64(sourceBytes / 4)
	d.outputPixels += outPixels
	d.outputBytes += outPixels * 4
	if now.Sub(d.started) >= time.Second {
		log.Printf("krkrgo runtime stretchCopy stats: calls=%d time=%s avg=%s max=%s source_pixels=%d source_bytes=%d output_pixels=%d output_bytes=%d elapsed=%s",
			d.calls, d.totalTime.Round(time.Microsecond), (d.totalTime / time.Duration(d.calls)).Round(time.Microsecond), d.maxTime.Round(time.Microsecond),
			d.sourcePixels, d.sourceBytes, d.outputPixels, d.outputBytes, now.Sub(d.started).Round(time.Millisecond))
		d.started = now
		d.calls, d.totalTime, d.maxTime = 0, 0, 0
		d.sourcePixels, d.sourceBytes, d.outputPixels, d.outputBytes = 0, 0, 0, 0
	}
	d.Unlock()
}
