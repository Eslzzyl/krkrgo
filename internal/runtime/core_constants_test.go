package runtime

import "testing"

func TestSessionRegistersOfficialCoreConstants(t *testing.T) {
	first := NewSession(DefaultServices(), 100_000)
	defer first.Close()
	second := NewSession(DefaultServices(), 100_000)
	defer second.Close()

	constants := []struct {
		expression string
		want       int64
	}{
		{"gcsAuto", -1},
		{"ltOpaque", 1},
		{"ltAlpha", 2},
		{"ltAdditive", 3},
		{"ltPsNormal", 13},
		{"omPsNormal", 13},
		{"imageTagLayerType.opaque.type", 1},
		{"imageTagLayerType.transparent.type", 2},
		{"imageTagLayerType.psburn.type", 23},
		{"mbMiddle", 2},
		{"VK_ESCAPE", 0x1b},
		{"VK_PADLEFT", 0x1b5},
		{"sgfmMuteOnDeactivate", 2},
		{"stBlackmanSinc", 18},
		{"stRefNoClip", 0x10000},
	}

	for _, session := range []*Session{first, second} {
		for _, constant := range constants {
			value, err := session.Evaluate(constant.expression)
			if err != nil {
				t.Fatalf("Evaluate(%q): %v", constant.expression, err)
			}
			got, err := value.Int()
			if err != nil || got != constant.want {
				t.Errorf("%s = %d, %v; want %d", constant.expression, got, err, constant.want)
			}
		}
	}

	if _, err := first.Evaluate("imageTagLayerType.opaque.type = 99"); err != nil {
		t.Fatalf("mutate first session imageTagLayerType: %v", err)
	}
	value, err := second.Evaluate("imageTagLayerType.opaque.type")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.Int(); err != nil || got != 1 {
		t.Fatalf("second session imageTagLayerType.opaque.type = %d, %v; want 1", got, err)
	}
}
