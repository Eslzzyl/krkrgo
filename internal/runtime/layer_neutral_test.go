package runtime

import (
	"bytes"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeLayerInitialAndResizedImagesUseReferenceNeutralColors(t *testing.T) {
	layer := nativeLayerDefaults(tjs.ObjectID(1))
	if got, want := layer.RGBA[:4], []byte{255, 255, 255, 0}; !bytes.Equal(got, want) {
		t.Fatalf("initial alpha-layer pixel = %v, want transparent white %v", got, want)
	}
	if err := layer.SetType(LayerAdditive); err != nil {
		t.Fatal(err)
	}
	if err := resizeLayerImage(layer, nativeLayerDefaultSize+1, nativeLayerDefaultSize+1); err != nil {
		t.Fatal(err)
	}
	index := (nativeLayerDefaultSize*nativeLayerDefaultSize + nativeLayerDefaultSize) * 4
	if got, want := layer.RGBA[index:index+4], []byte{0, 0, 0, 0}; !bytes.Equal(got, want) {
		t.Fatalf("resized additive-layer pixel = %v, want transparent black %v", got, want)
	}
}
