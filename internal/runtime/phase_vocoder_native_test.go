package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestPhaseVocoderNativeClassConfiguration(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()
	value, err := session.Evaluate(`var ctor = WaveSoundBuffer.PhaseVocoder; var v = new ctor(); v.window=256; v.overlap=8; v.pitch=1.25; v.time=0.5; [v instanceof ctor,v.window,v.overlap,v.pitch,v.time,typeof v.interface,v.interface!=0].join('|')`)
	if err != nil {
		t.Fatal(err)
	}
	// TJS has no Boolean value kind; comparison results stringify as Integer 0/1.
	if value.Kind() != tjs.KindString || value.Text() != "1|256|8|1.25|0.5|Integer|1" {
		t.Fatalf("PhaseVocoder script surface = %v, want expected constructor/configuration values", value)
	}
}
