package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

func TestNativeFontBoundPropertyReferenceKeepsDynamicMembers(t *testing.T) {
	session := NewSession(DefaultServices(), 10_000)
	defer session.Close()

	got, err := session.Evaluate(`
first = new Font();
second = new Font();
first.__propertyHook = %[value:4];
second.__propertyHook = %[value:9];
first.face = "first";
second.face = "second";
var reference = (&first.face) incontextof second;
var original = *reference;
*reference = "bound";
result = original == "second" && second.face == "bound" && first.face == "first" && first.__propertyHook.value == 4 && second.__propertyHook.value == 9;
`)
	if err != nil {
		t.Fatal(err)
	}
	got, err = session.Evaluate("result")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := got.Truth(); err != nil || !ok {
		t.Fatalf("bound native Font property reference = %v, %v; want true", got, err)
	}
}
func TestFontMapPrerenderedFontBindsStorageToReceiver(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "mapped.tft"), []byte("font data"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage, err := assets.OpenStorage(project, core.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	services := NewServices(storage, "", core.Limits{})
	session := NewSession(services, 10_000)
	defer session.Close()

	if _, err := session.Evaluate(`
first = new Font();
second = new Font();
first.mapPrerenderedFont("mapped.tft");
`); err != nil {
		t.Fatal(err)
	}
	firstValue, err := session.Evaluate("first")
	if err != nil {
		t.Fatal(err)
	}
	secondValue, err := session.Evaluate("second")
	if err != nil {
		t.Fatal(err)
	}
	if got := services.Font(firstValue.ObjectRef().Object).PrerenderedFontStorage; got != "mapped.tft" {
		t.Fatalf("first mapped font = %q, want mapped.tft", got)
	}
	if got := services.Font(secondValue.ObjectRef().Object).PrerenderedFontStorage; got != "" {
		t.Fatalf("second mapped font = %q, want no mapping", got)
	}
	if _, err := session.Evaluate(`first.mapPrerenderedFont("missing.tft")`); err == nil {
		t.Fatal("mapping a missing storage unexpectedly succeeded")
	}
	if got := services.Font(firstValue.ObjectRef().Object).PrerenderedFontStorage; got != "mapped.tft" {
		t.Fatalf("failed mapping changed receiver state to %q", got)
	}
}
