package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestWaveSoundBufferLabelsAreNamedDictionary(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	t.Cleanup(func() { _ = session.Close() })
	soundValue := evalSoundValue(t, session, "sound = new WaveSoundBuffer()")
	sound := session.Services.Sound(soundValue.ObjectRef().Object)
	if err := sound.OpenAudio(assets.Audio{
		Channels:   1,
		SampleRate: 44_100,
		Samples:    make([]int16, 88_200),
	}, assets.LoopInfo{Labels: []assets.Label{{Name: "start", Position: 44_100}}}); err != nil {
		t.Fatalf("open labeled sound: %v", err)
	}

	labels := evalSoundValue(t, session, "labels = sound.labels")
	if got, want := evalSoundText(t, session, "labels.start.name + \"|\" + labels.start.samplePosition + \"|\" + labels.start.position + \"|\" + (labels === sound.labels)"), "start|44100|1000|1"; got != want {
		t.Fatalf("named sound label = %q, want %q", got, want)
	}
	if value, err := session.Evaluate("labels.missing"); err != nil || !value.IsVoid() {
		t.Fatalf("missing sound label = %v, err=%v; want void", value, err)
	}
	if err := session.Services.invalidateWaveSoundLabels(session.VM, soundValue, &session.Budget); err != nil {
		t.Fatalf("invalidate sound labels: %v", err)
	}
	if valid, _ := session.VM.IsValid(labels).Int(); valid != 0 {
		t.Fatal("cached sound label dictionary remains valid after invalidation")
	}
	if value, err := session.Evaluate("sound.labels.start.samplePosition"); err != nil || value.IntegerValue() != 44_100 {
		t.Fatalf("rebuilt sound label position = %v, err=%v", value, err)
	}
}

func TestWaveSoundBufferMissingLabelOnEmptySound(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	t.Cleanup(func() { _ = session.Close() })
	evalSoundValue(t, session, "sound = new WaveSoundBuffer()")
	if labels := evalSoundValue(t, session, "sound.labels"); labels.Kind() != tjs.KindObject {
		t.Fatalf("sound.labels = %v, want a dictionary object", labels)
	}
	if value, err := session.Evaluate("sound.labels.start"); err != nil || !value.IsVoid() {
		t.Fatalf("missing label on empty sound = %v, err=%v; want void", value, err)
	}
}

func TestWaveSoundBufferOpenBytesWithoutSLIInvalidatesLabels(t *testing.T) {
	session := NewSession(DefaultServices(), 20_000)
	t.Cleanup(func() { _ = session.Close() })
	soundValue := evalSoundValue(t, session, "sound = new WaveSoundBuffer()")
	sound := session.Services.Sound(soundValue.ObjectRef().Object)
	if err := sound.OpenAudio(assets.Audio{
		Channels:   1,
		SampleRate: 44_100,
		Samples:    make([]int16, 882),
	}, assets.LoopInfo{Labels: []assets.Label{{Name: "start", Position: 441}}}); err != nil {
		t.Fatalf("open labeled sound: %v", err)
	}
	cached := evalSoundValue(t, session, "sound.labels")
	if _, err := session.Evaluate("sound.openBytes(" + tjsOctetLiteral(testTCWFMonoFixture()) + ")"); err != nil {
		t.Fatalf("open unlabeled TCWF sound: %v", err)
	}
	if valid, _ := session.VM.IsValid(cached).Int(); valid != 0 {
		t.Fatal("cached sound labels remain valid after opening an unlabeled stream")
	}
	if value, err := session.Evaluate("sound.labels.start"); err != nil || !value.IsVoid() {
		t.Fatalf("label after opening unlabeled stream = %v, err=%v; want void", value, err)
	}
}
