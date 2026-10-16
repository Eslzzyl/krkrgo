package runtime

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func captureStallTraceLog(t *testing.T, fn func()) string {
	t.Helper()
	var output bytes.Buffer
	previousWriter, previousFlags, previousPrefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	fn()
	return output.String()
}

func TestStallTraceThresholdFiltersShortEvents(t *testing.T) {
	t.Setenv("KRKRGO_STALL_TRACE", "1")
	t.Setenv("KRKRGO_STALL_THRESHOLD_MS", "5")
	output := captureStallTraceLog(t, func() {
		TraceStallFrame(1, 0, 4*time.Millisecond, 0, 0, 0, 0)
		TraceStallFrame(2, 16, 8*time.Millisecond, 7*time.Millisecond, 6*time.Millisecond, 0, 0)
	})
	if strings.Count(output, "krkrgo-stall type=frame") != 1 || !strings.Contains(output, "serial=2") {
		t.Fatalf("stall trace output = %q, want one long frame record", output)
	}
}

func TestStallTraceResourceIncludesLoadStages(t *testing.T) {
	t.Setenv("KRKRGO_STALL_TRACE", "1")
	t.Setenv("KRKRGO_STALL_THRESHOLD_MS", "1")
	output := captureStallTraceLog(t, func() {
		traceStallLayerLoad("scene", "scene.png", 16, 1024, 2048, 32, 16,
			2*time.Millisecond, 3*time.Millisecond, 4*time.Millisecond,
			5*time.Millisecond, 6*time.Millisecond, 7*time.Millisecond, 8*time.Millisecond)
	})
	for _, field := range []string{"kind=Layer.loadImages", "logical_ms=16", "decoded_bytes=2048", "read_us=2000", "decode_us=3000", "color_key_us=4000", "mask_us=5000", "province_us=6000", "metadata_us=7000", "commit_us=8000"} {
		if !strings.Contains(output, field) {
			t.Fatalf("stall resource output = %q, missing %q", output, field)
		}
	}
}
