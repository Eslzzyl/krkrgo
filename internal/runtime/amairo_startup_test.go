package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

// Amairo's startup scenario first shows the system cover
// ([syscover visible color=0xFFFFFF] in custom.ks), fades it out inside the
// caution block, and loads its notice image with [ev file=attention notrans].
// Two runtime behaviours made that sequence invisible and left the compositor
// emitting one unchanged opaque black frame:
//
//   - KAG tag elements carried no taglist, so the KAGEnvironment framework
//     refused to route environment object tags such as "ev" and never loaded
//     attention.png.
//   - Dictionary's assign/assignStruct/clear leaked onto Dictionary instances,
//     so KAG's attribute probe "elm.clear" looked set and the cover took its
//     clear branch instead of filling white.
func TestAmairoStartupCoverAndNoticeImage(t *testing.T) {
	const project = "../../testdata/amairo"
	if _, err := os.Stat(project); err != nil {
		t.Skipf("amairo testdata unavailable: %v", err)
	}
	session, err := OpenSession(project, "", 1<<30)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	defer session.Close()
	if _, err := session.Startup(); err != nil {
		t.Fatalf("startup: %v", err)
	}

	blackHash := ""
	sawWhiteFrame := false
	for ms := uint64(100); ms <= 6500; ms += 100 {
		if err := session.Tick(ms); err != nil {
			t.Fatalf("tick %d ms: %v", ms, err)
		}
		frame, err := session.Frame()
		if err != nil {
			t.Fatalf("frame at %d ms: %v", ms, err)
		}
		sum := sha256.Sum256(frame.RGBA)
		digest := hex.EncodeToString(sum[:])
		if blackHash == "" {
			blackHash = digest
		} else if digest != blackHash {
			// The startup sequence must no longer compose a single frame.
			sawWhiteFrame = sawWhiteFrame || uniformPixel(frame, 640, 360, 255, 255, 255)
		}
	}
	if !sawWhiteFrame {
		t.Fatal("startup never composed the white system cover frame")
	}

	imageFile, err := session.Evaluate("typeof world_object.env.objects['ev'] == 'Object' ? world_object.env.objects['ev'].imageFile : ''")
	if err != nil {
		t.Fatalf("inspect ev environment object: %v", err)
	}
	if got := imageFile.Text(); got != "attention" {
		t.Fatalf("ev environment object image file = %q, want %q", got, "attention")
	}
	disp, err := session.Evaluate("typeof world_object.env.objects['ev'] == 'Object' ? world_object.env.objects['ev'].disp : -1")
	if err != nil {
		t.Fatalf("inspect ev environment display state: %v", err)
	}
	if n, err := disp.Int(); err != nil || n <= 0 {
		t.Fatalf("ev environment object display state = %s, want a shown state", disp.Text())
	}
}

func uniformPixel(frame FrameSnapshot, x, y int32, r, g, b byte) bool {
	if x < 0 || y < 0 || x >= frame.Width || y >= frame.Height {
		return false
	}
	i := (int(y)*int(frame.Width) + int(x)) * 4
	return frame.RGBA[i] == r && frame.RGBA[i+1] == g && frame.RGBA[i+2] == b && frame.RGBA[i+3] == 255
}
