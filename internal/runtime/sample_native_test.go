package runtime

import (
	"math"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestGetSamplePluginPropertiesAndStaticDefaults(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<sample-plugin-setup>", `Plugins.link("getSample.dll"); WaveSoundBuffer.setDefaultCounts(3); WaveSoundBuffer.setDefaultAheads(1); sound = new WaveSoundBuffer()`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`sound.sampleCount * 100 + sound.sampleAhead`)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 301 {
		t.Fatalf("default sample config = %d, want count=3/ahead=1", got)
	}
	if _, err := session.executeSource("<sample-plugin-set-properties>", `sound.sampleCount = 2; sound.sampleAhead = 0`, false); err != nil {
		t.Fatal(err)
	}
	if got := evalSoundInt(t, session, "sound.sampleCount"); got != 2 {
		t.Fatalf("sampleCount = %d, want 2", got)
	}
	if got := evalSoundInt(t, session, "sound.sampleAhead"); got != 0 {
		t.Fatalf("sampleAhead = %d, want 0", got)
	}
}

func TestGetSampleValueUsesDecodedPCMWithoutMovingPlayback(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<sample-plugin-setup>", `Plugins.link("getSample.dll"); sound = new WaveSoundBuffer(); sound.sampleCount = 2; sound.sampleAhead = 1`, false); err != nil {
		t.Fatal(err)
	}
	idValue, err := session.Evaluate(`sound`)
	if err != nil {
		t.Fatal(err)
	}
	sound := session.Services.Sound(idValue.ObjectRef().Object)
	if sound == nil {
		t.Fatal("native sound was not registered")
	}
	if err := sound.OpenAudio(assets.Audio{Channels: 1, SampleRate: 48_000, Samples: []int16{3277, -16384, 8192, 30000}}, assets.LoopInfo{}); err != nil {
		t.Fatal(err)
	}
	if err := sound.SetSamplePosition(1); err != nil {
		t.Fatal(err)
	}
	before := sound.SamplePosition()
	value, err := session.Evaluate(`sound.sampleValue`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Float()
	if err != nil {
		t.Fatal(err)
	}
	want := math.Pow(30000.0/32768.0, 2)
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("sampleValue = %.15f, want %.15f", got, want)
	}
	if sound.SamplePosition() != before {
		t.Fatalf("sampling moved playback from %d to %d", before, sound.SamplePosition())
	}
	if !sound.UseVisBuffer() {
		t.Fatal("sampleValue did not enable useVisBuffer")
	}
	if value.Kind() != tjs.KindReal {
		t.Fatalf("sampleValue kind = %v, want real", value.Kind())
	}
}

func TestGetSampleDefaultConfigAppliedLazily(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	if _, err := session.executeSource("<sample-plugin-lazy-defaults>", `sound = new WaveSoundBuffer(); Plugins.link("getSample.dll"); WaveSoundBuffer.setDefaultCounts(7); WaveSoundBuffer.setDefaultAheads(-2)`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate(`sound.sampleCount * 100 + sound.sampleAhead`)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := value.Int(); got != 698 {
		t.Fatalf("lazy defaults = %d, want count=7/ahead=-2", got)
	}
}
