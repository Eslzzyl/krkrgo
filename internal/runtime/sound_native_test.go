package runtime

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/media/ffmpeg"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func requireSidecarWAV(t *testing.T) {
	t.Helper()
	if _, err := ffmpeg.DecodeAudioBytes("<test>", testRuntimeWave(1), 10); err != nil {
		t.Skipf("FFmpeg WAV decoding unavailable: %v", err)
	}
}

func TestWaveSoundBufferNativeConstructionAndProperties(t *testing.T) {
	s := NewSession(DefaultServices(), 10_000)
	sound := evalSoundValue(t, s, "sound = new WaveSoundBuffer()")
	if sound.Kind() != tjs.KindObject || sound.IsNull() {
		t.Fatalf("new WaveSoundBuffer() = %v", sound)
	}
	id := sound.ObjectRef().Object
	if id == 0 || s.Services.Sound(id) == nil {
		t.Fatalf("constructed sound was not registered: id=%d sounds=%d", id, len(s.Services.Sounds))
	}
	if got := evalSoundText(t, s, "sound.status"); got != SoundUnload {
		t.Fatalf("initial status = %q, want %q", got, SoundUnload)
	}
	if got := evalSoundInt(t, s, "sound.volume"); got != 100_000 {
		t.Fatalf("initial volume = %d", got)
	}

	for _, source := range []string{
		"sound.volume = 45000",
		"sound.volume2 = 55000",
		"sound.pan = -120000",
		"sound.paused = true",
		"sound.looping = true",
		"sound.useVisBuffer = true",
	} {
		evalSoundValue(t, s, source)
	}
	if got := evalSoundInt(t, s, "sound.volume"); got != 45_000 {
		t.Fatalf("volume = %d", got)
	}
	if got := evalSoundInt(t, s, "sound.volume2"); got != 55_000 {
		t.Fatalf("volume2 = %d", got)
	}
	if got := evalSoundInt(t, s, "sound.pan"); got != -100_000 {
		t.Fatalf("pan clamp = %d", got)
	}
	if got := evalSoundInt(t, s, "sound.paused"); got != 1 {
		t.Fatalf("paused = %d", got)
	}
	if got := evalSoundInt(t, s, "sound.looping"); got != 1 {
		t.Fatalf("looping = %d", got)
	}
	if got := evalSoundInt(t, s, "sound.useVisBuffer"); got != 1 {
		t.Fatalf("useVisBuffer = %d", got)
	}
}

func TestWaveSoundBufferFiltersRemainMutableAcrossReads(t *testing.T) {
	s := NewSession(DefaultServices(), 10_000)
	defer s.Close()
	evalSoundValue(t, s, "sound = new WaveSoundBuffer()")

	first := evalSoundValue(t, s, "sound.filters")
	if first.Kind() != tjs.KindObject || first.IsNull() {
		t.Fatalf("sound.filters = %v; want an array object", first)
	}
	evalSoundValue(t, s, "sound.filters.assign([17, 29])")
	second := evalSoundValue(t, s, "sound.filters")
	if second.Kind() != tjs.KindObject || second.IsNull() || first.ObjectRef().Object != second.ObjectRef().Object {
		t.Fatalf("sound.filters object changed between reads: first=%v second=%v", first, second)
	}
	for index, want := range []int64{17, 29} {
		if got := evalSoundInt(t, s, fmt.Sprintf("sound.filters[%d]", index)); got != want {
			t.Errorf("sound.filters[%d] = %d, want %d", index, got, want)
		}
	}
}

func TestWaveSoundBufferNativeWaveTransportAndFade(t *testing.T) {
	requireSidecarWAV(t)
	s := NewSession(DefaultServices(), 20_000)
	evalSoundValue(t, s, "sound = new WaveSoundBuffer()")
	wave := testRuntimeWave(882)
	if _, err := s.Evaluate("sound.openBytes(" + tjsOctetLiteral(wave) + ")"); err != nil {
		t.Fatalf("open WAVE: %v", err)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch open event: %v", err)
	}
	if got := evalSoundText(t, s, "sound.status"); got != SoundStop {
		t.Fatalf("open status = %q", got)
	}
	for _, tc := range []struct {
		expr string
		want int64
	}{
		{"sound.frequency", 44_100},
		{"sound.bits", 16},
		{"sound.channels", 1},
		{"sound.totalTime", 20},
		{"sound.position", 0},
		{"sound.samplePosition", 0},
	} {
		if got := evalSoundInt(t, s, tc.expr); got != tc.want {
			t.Errorf("%s = %d, want %d", tc.expr, got, tc.want)
		}
	}

	for _, source := range []string{
		"sound.looping = true",
		"sound.paused = false",
		"sound.volume = 45000",
		"sound.play()",
	} {
		evalSoundValue(t, s, source)
	}
	if err := s.AdvanceClock(0); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch play event: %v", err)
	}
	if got := evalSoundText(t, s, "sound.status"); got != SoundPlay {
		t.Fatalf("play status = %q", got)
	}

	evalSoundValue(t, s, "sound.setSamplePosition(100)")
	if got := evalSoundInt(t, s, "sound.samplePosition"); got != 100 {
		t.Fatalf("sample position = %d", got)
	}
	evalSoundValue(t, s, "sound.setPos(10)")
	if got := evalSoundInt(t, s, "sound.position"); got != 10 {
		t.Fatalf("position = %d, want 10", got)
	}

	evalSoundValue(t, s, "sound.fade(0, 180)")
	if err := s.AdvanceClock(60); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch first fade beat: %v", err)
	}
	if got := evalSoundInt(t, s, "sound.volume"); got != 30_000 {
		t.Fatalf("volume after one fade beat = %d", got)
	}
	if err := s.AdvanceClock(180); err != nil {
		t.Fatal(err)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch completed fade: %v", err)
	}
	if got := evalSoundInt(t, s, "sound.volume"); got != 0 {
		t.Fatalf("volume after fade = %d", got)
	}

	evalSoundValue(t, s, "sound.stop()")
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch stop event: %v", err)
	}
	if got := evalSoundText(t, s, "sound.status"); got != SoundStop {
		t.Fatalf("stop status = %q", got)
	}
	if got := evalSoundInt(t, s, "sound.samplePosition"); got != 0 {
		t.Fatalf("stop sample position = %d", got)
	}
}

func TestDispatchEventsFindsInheritedSoundCallback(t *testing.T) {
	requireSidecarWAV(t)
	s := NewSession(DefaultServices(), 10_000)
	if _, err := s.executeSource("sound-callback-test.tjs", `
class SoundStatusBase extends WaveSoundBuffer {
	function onStatusChanged(status) { global.statusSeen = status; }
}
class SoundStatusChild extends SoundStatusBase {}
global.statusSeen = "";
global.sound = new SoundStatusChild();
`, false); err != nil {
		t.Fatalf("define sound callback subclass: %v", err)
	}
	wave := tjsOctetLiteral(testRuntimeWave(882))
	evalSoundValue(t, s, "global.sound.openBytes("+wave+")")
	evalSoundValue(t, s, "global.sound.play()")
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch inherited play callback: %v", err)
	}
	if got := evalSoundText(t, s, "global.statusSeen"); got != SoundPlay {
		t.Fatalf("inherited play callback status = %q, want %q", got, SoundPlay)
	}
	evalSoundValue(t, s, "global.sound.stop()")
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch inherited stop callback: %v", err)
	}
	if got := evalSoundText(t, s, "global.statusSeen"); got != SoundStop {
		t.Fatalf("inherited stop callback status = %q, want %q", got, SoundStop)
	}
}
func TestSystemSeWaitProgressesForMissingFailedAndFinishedAudio(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("system-se-wait-test.tjs", `
class SystemVoice extends WaveSoundBuffer {
	function SetState(mode) {
		if (mode == 3) return this.paused ? "susppend" : this.status;
	}
	function ShouldContinueAfterSystemSeWait() {
		return this.SetState(3) != "play";
	}
	function onStatusChanged(status) { global.lastSystemVoiceStatus = status; }
}
global.systemVoice = new SystemVoice();
global.lastSystemVoiceStatus = "";
`, false); err != nil {
		t.Fatalf("define system voice: %v", err)
	}

	continues := func() bool {
		value, err := session.Evaluate("global.systemVoice.ShouldContinueAfterSystemSeWait()")
		if err != nil {
			t.Fatalf("evaluate SystemSeWait condition: %v", err)
		}
		result, err := value.Truth()
		if err != nil {
			t.Fatalf("read SystemSeWait condition: %v", err)
		}
		return result
	}
	if !continues() {
		t.Fatal("unloaded/missing system voice blocked SystemSeWait")
	}
	voiceValue, err := session.Evaluate("global.systemVoice")
	if err != nil {
		t.Fatal(err)
	}
	voice := session.Services.Sound(voiceValue.ObjectRef().Object)
	// A malformed Ogg source fails when the sidecar is present and degrades to
	// a no-op when it is absent; either way the wait must stay responsive.
	_ = voice.OpenBytes([]byte("OggS"), assets.LoopInfo{})
	if !continues() {
		t.Fatal("failed system voice blocked SystemSeWait")
	}

	requireSidecarWAV(t)
	evalSoundValue(t, session, "global.systemVoice.openBytes("+tjsOctetLiteral(testRuntimeWave(882))+")")
	evalSoundValue(t, session, "global.systemVoice.play()")
	if err := session.DispatchEvents(); err != nil {
		t.Fatalf("dispatch system voice play event: %v", err)
	}
	if got := evalSoundText(t, session, "global.lastSystemVoiceStatus"); got != SoundPlay {
		t.Fatalf("play status callback = %q, want %q", got, SoundPlay)
	}
	if continues() {
		t.Fatal("SystemSeWait continued while system voice was playing")
	}

	if err := session.Tick(20); err != nil {
		t.Fatalf("tick through system voice EOF: %v", err)
	}
	if !continues() {
		t.Fatal("completed system voice blocked SystemSeWait")
	}
	if got := evalSoundText(t, session, "global.lastSystemVoiceStatus"); got != SoundStop {
		t.Fatalf("EOF status callback = %q, want %q", got, SoundStop)
	}
}

func TestWaveSoundBufferNativeImmediateSubBeatFadeEvent(t *testing.T) {
	s := NewSession(DefaultServices(), 10_000)
	sound := evalSoundValue(t, s, "sound = new WaveSoundBuffer()")
	id := sound.ObjectRef().Object

	evalSoundValue(t, s, "sound.fade(0, 30)")
	pending := s.Services.SoundEvents[id]
	if len(pending) != 1 || pending[0].Type != "onFadeCompleted" {
		t.Fatalf("immediate fade events = %v, want one completion", pending)
	}
	if events := s.Services.Sound(id).Events(); len(events) != 0 {
		t.Fatalf("immediate fade left undelivered sound events: %v", events)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch immediate fade event: %v", err)
	}
	if pending := s.Services.SoundEvents[id]; len(pending) != 0 {
		t.Fatalf("immediate fade events after dispatch = %v", pending)
	}
}

func TestWaveSoundBufferNativeReplacingFadeQueuesCompletion(t *testing.T) {
	s := NewSession(DefaultServices(), 10_000)
	sound := evalSoundValue(t, s, "sound = new WaveSoundBuffer()")
	id := sound.ObjectRef().Object

	evalSoundValue(t, s, "sound.fade(0, 120)")
	evalSoundValue(t, s, "sound.fade(100000, 120)")
	pending := s.Services.SoundEvents[id]
	if len(pending) != 1 || pending[0].Type != "onFadeCompleted" {
		t.Fatalf("replaced fade events = %v, want one completion", pending)
	}
	if events := s.Services.Sound(id).Events(); len(events) != 0 {
		t.Fatalf("replaced fade left undelivered sound events: %v", events)
	}
	if err := s.DispatchEvents(); err != nil {
		t.Fatalf("dispatch replaced fade event: %v", err)
	}
	if err := s.AdvanceClock(120); err != nil {
		t.Fatalf("advance replacement fade: %v", err)
	}
	pending = s.Services.SoundEvents[id]
	if len(pending) != 1 || pending[0].Type != "onFadeCompleted" {
		t.Fatalf("replacement completion events = %v, want one completion", pending)
	}
}

func TestWaveSoundBufferOpenDegradesWithoutSidecar(t *testing.T) {
	if _, err := ffmpeg.DefaultLoader(); err == nil {
		t.Skip("FFmpeg sidecar is available")
	}
	sound := NewWaveSoundBuffer()
	if err := sound.OpenBytes([]byte("OggS"), assets.LoopInfo{}); err != nil {
		t.Fatalf("open without sidecar = %v; want nil", err)
	}
	if sound.Status() != SoundUnload {
		t.Fatalf("degraded open status = %q; want %q", sound.Status(), SoundUnload)
	}
	if err := sound.Play(); err != nil {
		t.Fatalf("play after degraded open = %v; want nil", err)
	}
}

func TestWaveSoundBufferNativeMalformedMissingAndUnsupportedDevice(t *testing.T) {
	s := NewSession(DefaultServices(), 10_000)
	evalSoundValue(t, s, "sound = new WaveSoundBuffer()")

	if _, err := s.Evaluate("sound.openBytes(" + tjsOctetLiteral([]byte("RIFF")) + ")"); err != nil {
		if _, loadErr := ffmpeg.DefaultLoader(); loadErr == nil {
			if !errors.Is(err, ffmpeg.ErrMalformed) {
				t.Fatalf("malformed WAVE error = %v", err)
			}
		} else {
			t.Fatalf("malformed WAVE without sidecar = %v; want no-op", err)
		}
	}
	if _, err := s.Evaluate(`sound.open("missing.wav")`); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("missing source error = %v", err)
	}
	for _, expression := range []string{"sound.setDevice()", "sound.getDevice()"} {
		if _, err := s.Evaluate(expression); !errors.Is(err, ErrUnsupportedDevice) {
			t.Errorf("%s error = %v, want ErrUnsupportedDevice", expression, err)
		}
	}
	if _, err := s.Evaluate("sound.setPos(1)"); err == nil {
		t.Fatal("seeking an unloaded sound unexpectedly succeeded")
	}
}

func TestWaveSoundBufferNativeGlobalControlsAndStopAll(t *testing.T) {
	requireSidecarWAV(t)
	s := NewSession(DefaultServices(), 50_000)
	wave := tjsOctetLiteral(testRuntimeWave(882))
	for _, source := range []string{
		"a = new WaveSoundBuffer()",
		"b = new WaveSoundBuffer()",
		"a.openBytes(" + wave + ")",
		"b.openBytes(" + wave + ")",
		"a.play()",
		"b.play()",
	} {
		evalSoundValue(t, s, source)
	}
	if got := evalSoundText(t, s, "a.status + \":\" + b.status"); got != SoundPlay+":"+SoundPlay {
		t.Fatalf("playing statuses = %q", got)
	}

	evalSoundValue(t, s, "WaveSoundBuffer.globalVolume = -1")
	if err := s.Tick(1); err != nil {
		t.Fatalf("tick at muted global volume: %v", err)
	}
	if output := s.TakeAudio(); len(output) == 0 || !allAudioZero(output) {
		t.Fatalf("muted global output = %v", output[:minAudioTestInt(len(output), 4)])
	}
	evalSoundValue(t, s, "WaveSoundBuffer.globalVolume = 100001")
	if err := s.Tick(2); err != nil {
		t.Fatalf("tick at full global volume: %v", err)
	}
	if output := s.TakeAudio(); len(output) == 0 || allAudioZero(output) {
		t.Fatalf("full global output was silent")
	}

	if got := evalSoundInt(t, s, "WaveSoundBuffer.globalVolume"); got != 100_000 {
		t.Fatalf("upper global volume clamp = %d, want 100000", got)
	}
	evalSoundValue(t, s, "a.setGlobalVolume(25000)")
	if got := evalSoundInt(t, s, "a.getGlobalVolume()"); got != 25_000 {
		t.Fatalf("instance global volume = %d, want 25000", got)
	}

	evalSoundValue(t, s, "a.stopAll()")
	if got := evalSoundText(t, s, "a.status + \":\" + b.status"); got != SoundStop+":"+SoundStop {
		t.Fatalf("stopAll statuses = %q", got)
	}
	if _, err := s.Evaluate("WaveSoundBuffer.globalFocusMode = 1"); !errors.Is(err, ErrUnsupportedDevice) {
		t.Fatalf("unsupported focus mode error = %v", err)
	}
}

func evalSoundValue(t *testing.T, s *Session, source string) tjs.Value {
	t.Helper()
	value, err := s.Evaluate(source)
	if err != nil {
		t.Fatalf("evaluate %q: %v", source, err)
	}
	return value
}

func evalSoundInt(t *testing.T, s *Session, source string) int64 {
	t.Helper()
	value := evalSoundValue(t, s, source)
	got, err := value.Int()
	if err != nil {
		t.Fatalf("%q result %v: %v", source, value, err)
	}
	return got
}

func evalSoundText(t *testing.T, s *Session, source string) string {
	t.Helper()
	return evalSoundValue(t, s, source).Text()
}

func tjsOctetLiteral(data []byte) string {
	var b strings.Builder
	b.WriteString("<%")
	for i, x := range data {
		if i != 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, " %02x", x)
	}
	b.WriteString(" %>")
	return b.String()
}

func testRuntimeWave(frames int) []byte {
	pcm := make([]byte, frames*2)
	for i := range frames {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16((i%200)-100)))
	}
	out := make([]byte, 44+len(pcm))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], 1)
	binary.LittleEndian.PutUint32(out[24:28], 44_100)
	binary.LittleEndian.PutUint32(out[28:32], 88_200)
	binary.LittleEndian.PutUint16(out[32:34], 2)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}
