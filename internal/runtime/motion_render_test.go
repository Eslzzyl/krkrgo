package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func TestBlendMotionImageLinearFilterPreservesTransparentEdgeColor(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 0},
	}
	dst := make([]byte, 2*4)
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}
	if err := blendMotionImage(dst, 2, 1, src, 1.5, 0.5, 1, nil, false, ctx); err != nil {
		t.Fatalf("blend motion image: %v", err)
	}
	if got := dst[:4]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 128 {
		t.Fatalf("linear sample = %v, want premultiplied-alpha red at half opacity", got)
	}
}

func TestBlendMotionImageIdentityTranslationUsesSourcePixels(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 0},
	}
	dst := []byte{0, 255, 0, 255, 0, 255, 0, 255}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}

	if err := blendMotionImage(dst, 2, 1, src, 1, 0.5, 1, nil, false, ctx); err != nil {
		t.Fatalf("blend motion image: %v", err)
	}
	if got := dst[:4]; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("identity sample = %v, want opaque red", got)
	}
	if got := dst[4:]; got[0] != 0 || got[1] != 255 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("transparent source changed destination = %v", got)
	}
}

func TestBlendMotionImageOpaqueIdentityCopiesRows(t *testing.T) {
	src := &assets.Image{
		Width:  2,
		Height: 1,
		RGBA:   []byte{255, 0, 0, 255, 0, 0, 255, 255},
	}
	dst := []byte{0, 255, 0, 255, 0, 255, 0, 255}
	ctx := motionRenderContext{opacity: 255, matrix: identityMotionMatrix}

	if err := blendMotionImage(dst, 2, 1, src, 1, 0.5, 1, nil, true, ctx); err != nil {
		t.Fatalf("blend opaque motion image: %v", err)
	}
	if got := dst; got[0] != 255 || got[1] != 0 || got[2] != 0 || got[3] != 255 ||
		got[4] != 0 || got[5] != 0 || got[6] != 255 || got[7] != 255 {
		t.Fatalf("opaque identity copy = %v, want source pixels", got)
	}
}
