package runtime

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func TestNativeLayerImageModifiedProperty(t *testing.T) {
	services := DefaultServices()
	session := NewSession(services, 10_000)
	if _, err := session.executeSource("<layer-image-modified-test>", `
var l = new Layer();
var initial = l.imageModified;
l.imageModified = true;
var assigned = l.imageModified;
l.imageModified = false;
l.setImageSize(2, 2);
var resized = l.imageModified;
l.imageModified = false;
l.setMainPixel(0, 0, 0x123456);
var painted = l.imageModified;
l.imageModified = false;
l.setMaskPixel(1, 1, 128);
var masked = l.imageModified;
l.imageModified = false;
l.fillRect(0, 0, 1, 1, 0xffabcdef);
var filled = l.imageModified;
l.imageModified = false;
var cleared = l.imageModified;
`, false); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int64{"initial": 0, "assigned": 1, "resized": 1, "painted": 1, "masked": 1, "filled": 1, "cleared": 0} {
		value, err := session.Evaluate(key)
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if value.Kind() != tjs.KindInteger || value.IntegerValue() != want {
			t.Errorf("%s = %#v, want integer %d", key, value, want)
		}
	}
}
