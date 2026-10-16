package host

import (
	"testing"

	runtime "github.com/eslzzyl/krkrgo/internal/runtime"
)

func BenchmarkUpdatePresentation720pStableRenderState(b *testing.B) {
	frame := runtime.FrameSnapshot{
		Width:                1280,
		Height:               720,
		RGBA:                 make([]byte, 1280*720*4),
		RenderStateHash:      1,
		RenderStateHashValid: true,
	}
	for i := 0; i < len(frame.RGBA); i += 4 {
		frame.RGBA[i+0], frame.RGBA[i+1], frame.RGBA[i+2], frame.RGBA[i+3] = 32, 64, 96, 255
	}
	game := &Game{}
	if err := game.updatePresentation(frame); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(frame.RGBA)))
	for b.Loop() {
		if err := game.updatePresentation(frame); err != nil {
			b.Fatal(err)
		}
	}
}
