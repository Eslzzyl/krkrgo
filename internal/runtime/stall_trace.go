package runtime

import (
	"log"
	"os"
	"strconv"
	"time"
)

const defaultStallTraceThreshold = 20 * time.Millisecond

// StallTraceEnabled reports whether detailed frame and resource timing is
// enabled. The trace is intentionally opt-in because it records clock reads
// and writes diagnostic lines.
func StallTraceEnabled() bool { return os.Getenv("KRKRGO_STALL_TRACE") == "1" }

// StallTraceThreshold returns the configured stall threshold in milliseconds.
// Invalid, zero, and negative values use the conservative default.
func StallTraceThreshold() time.Duration {
	text := os.Getenv("KRKRGO_STALL_THRESHOLD_MS")
	if text == "" {
		return defaultStallTraceThreshold
	}
	milliseconds, err := strconv.ParseFloat(text, 64)
	if err != nil || milliseconds <= 0 {
		return defaultStallTraceThreshold
	}
	return time.Duration(milliseconds * float64(time.Millisecond))
}

func stallTraceDuration(value time.Duration) int64 { return value.Microseconds() }

// TraceStallFrame writes one record when a frame interval or measured stage
// crosses the configured threshold. Durations are integer microseconds so the
// output remains easy to filter and aggregate.
func TraceStallFrame(serial, logicalMS uint64, frameInterval, update, tick, audio, presentation time.Duration) {
	if !StallTraceEnabled() {
		return
	}
	threshold := StallTraceThreshold()
	if frameInterval < threshold && update < threshold && tick < threshold && audio < threshold && presentation < threshold {
		return
	}
	log.Printf("krkrgo-stall type=frame serial=%d logical_ms=%d frame_interval_us=%d update_us=%d tick_us=%d audio_us=%d presentation_us=%d threshold_us=%d",
		serial, logicalMS, stallTraceDuration(frameInterval), stallTraceDuration(update), stallTraceDuration(tick), stallTraceDuration(audio), stallTraceDuration(presentation), stallTraceDuration(threshold))
}

// TraceStallDraw records a long gap between Draw callbacks or an expensive
// DrawImage call. Draw timing is separate from Update timing because the
// platform callback can stall after runtime work has completed.
func TraceStallDraw(serial uint64, drawInterval, draw time.Duration) {
	if !StallTraceEnabled() {
		return
	}
	threshold := StallTraceThreshold()
	if drawInterval < threshold && draw < threshold {
		return
	}
	log.Printf("krkrgo-stall type=draw serial=%d draw_interval_us=%d draw_us=%d threshold_us=%d",
		serial, stallTraceDuration(drawInterval), stallTraceDuration(draw), stallTraceDuration(threshold))
}

func traceStallLayerLoad(name, resolved string, logicalMS uint64, sourceBytes, decodedBytes int, width, height int32, read, decode, colorKey, mask, province, metadata, commit time.Duration) {
	if !StallTraceEnabled() {
		return
	}
	total := read + decode + colorKey + mask + province + metadata + commit
	if total < StallTraceThreshold() {
		return
	}
	log.Printf("krkrgo-stall type=resource kind=Layer.loadImages name=%q resolved=%q logical_ms=%d bytes=%d decoded_bytes=%d width=%d height=%d read_us=%d decode_us=%d color_key_us=%d mask_us=%d province_us=%d metadata_us=%d commit_us=%d total_us=%d threshold_us=%d",
		name, resolved, logicalMS, sourceBytes, decodedBytes, width, height, stallTraceDuration(read), stallTraceDuration(decode), stallTraceDuration(colorKey), stallTraceDuration(mask), stallTraceDuration(province), stallTraceDuration(metadata), stallTraceDuration(commit), stallTraceDuration(total), stallTraceDuration(StallTraceThreshold()))
}

func traceStallBitmapLoad(name string, logicalMS uint64, sourceBytes int, width, height uint32, read, decode, commit time.Duration, failed bool) {
	if !StallTraceEnabled() {
		return
	}
	total := read + decode + commit
	if total < StallTraceThreshold() {
		return
	}
	log.Printf("krkrgo-stall type=resource kind=Bitmap.loadAsync name=%q logical_ms=%d bytes=%d width=%d height=%d read_us=%d decode_us=%d commit_us=%d total_us=%d failed=%t threshold_us=%d",
		name, logicalMS, sourceBytes, width, height, stallTraceDuration(read), stallTraceDuration(decode), stallTraceDuration(commit), stallTraceDuration(total), failed, stallTraceDuration(StallTraceThreshold()))
}
