package runtime

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/eslzzyl/krkrgo/internal/assets"
)

func testVideo(t *testing.T) *VideoOverlay {
	t.Helper()
	v := NewVideoOverlay()
	if err := v.SetSourceMetadata(VideoSource{Name: "memory.mpg", Format: "MPEG", Width: 2, Height: 1, FPS: 2, Frames: 4, DurationMS: 2000}); err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 4; i++ {
		if err := v.QueueFrame(VideoFrame{Index: i, Width: 2, Height: 1, RGBA: []byte{byte(i), 0, 0, 255, byte(i), 0, 0, 255}}); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func TestVideoPropertiesAndLayerAssociation(t *testing.T) {
	v := testVideo(t)
	if err := v.SetRate(0); err == nil {
		t.Fatal("zero rate accepted")
	}
	if err := v.SetRate(2); err != nil {
		t.Fatal(err)
	}
	if err := v.SetVolume(100001); err == nil {
		t.Fatal("invalid volume accepted")
	}
	if err := v.SetBalance(-100001); err == nil {
		t.Fatal("invalid balance accepted")
	}
	layer := NewLayer(7)
	if err := v.SetLayer(0, layer); err != nil {
		t.Fatal(err)
	}
	if err := v.SetFrame(1); err != nil {
		t.Fatal(err)
	}
	if got := layer.RGBA[0]; got != 1 {
		t.Fatalf("layer frame pixel = %d", got)
	}
	if layer.Bounds.Width != 2 || layer.Bounds.Height != 1 {
		t.Fatalf("layer bounds = %+v", layer.Bounds)
	}
	v.SetVisible(false)
	if layer.Visible {
		t.Fatal("layer remained visible")
	}
}

func TestVideoClockEventsAndEOF(t *testing.T) {
	v := testVideo(t)
	initial := v.Events()
	if len(initial) != 1 || initial[0].Kind != "status" || initial[0].Status != VideoStopped {
		t.Fatalf("initial events = %+v", initial)
	}
	if err := v.Play(); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(0); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(500); err != nil {
		t.Fatal(err)
	}
	if v.Frame() != 1 || v.Position() != 500 {
		t.Fatalf("clock state frame=%d position=%d", v.Frame(), v.Position())
	}
	events := v.Events()
	if len(events) != 2 || events[0].Kind != "status" || events[0].Status != VideoPlaying || events[1].Kind != "frame" || events[1].Frame != 1 {
		t.Fatalf("events = %+v", events)
	}
	if err := v.Advance(2500); err != nil {
		t.Fatal(err)
	}
	if v.Status() != VideoStopped || v.Position() != 2500 || v.Frame() != 3 {
		t.Fatalf("EOF state frame=%d position=%d status=%q", v.Frame(), v.Position(), v.Status())
	}
	eofEvents := v.Events()
	if len(eofEvents) != 2 || eofEvents[0].Kind != "frame" || eofEvents[0].Frame != 3 || eofEvents[1].Kind != "status" || eofEvents[1].Status != VideoStopped {
		t.Fatalf("EOF events = %+v", eofEvents)
	}
	if err := v.Advance(2000); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("backward clock = %v", err)
	}
}

func TestVideoLoopSegmentSeekAndPeriodOrdering(t *testing.T) {
	v := testVideo(t)
	v.Events()
	if err := v.SetSegmentLoop(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := v.Play(); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(0); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(2000); err != nil {
		t.Fatal(err)
	}
	if v.Frame() != 2 || v.Position() != 1000 || v.Status() != VideoPlaying {
		t.Fatalf("segment state frame=%d position=%d status=%q", v.Frame(), v.Position(), v.Status())
	}
	events := v.Events()
	if len(events) != 3 || events[0].Kind != "status" || events[0].Status != VideoPlaying || events[1].Kind != "period" || events[1].Reason != 3 || events[2].Kind != "frame" || events[2].Frame != 2 {
		t.Fatalf("segment events = %+v", events)
	}
	v.CancelSegmentLoop()
	v.SetLoop(true)
	if err := v.Seek(0); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(4000); err != nil {
		t.Fatal(err)
	}
	if v.Status() != VideoPlaying || v.Position() != 0 || v.Frame() != 0 {
		t.Fatalf("loop state frame=%d position=%d status=%q", v.Frame(), v.Position(), v.Status())
	}
	loopEvents := v.Events()
	if len(loopEvents) != 1 || loopEvents[0].Kind != "period" || loopEvents[0].Reason != 0 {
		t.Fatalf("loop events = %+v", loopEvents)
	}
	if err := v.SetFrame(3); err != nil {
		t.Fatal(err)
	}
	if v.Frame() != 3 {
		t.Fatal("seek frame not applied")
	}
}

func TestVideoWindowsMediaUsesEmptyStub(t *testing.T) {
	const asfHeader = "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c"
	tests := []struct {
		name   string
		source string
		data   []byte
	}{
		{name: "signature without media extension", source: "movies/logo.bin", data: []byte(asfHeader)},
		{name: "WMV extension", source: "movies/LoGo.WmV", data: []byte("not an MPEG stream")},
		{name: "ASF extension", source: "movies/clip.AsF", data: []byte("not an MPEG stream")},
		{name: "WMA extension", source: "movies/audio.WmA", data: []byte("not an MPEG stream")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := NewVideoOverlay()
			if err := v.Open(tt.source, tt.data); err != nil {
				t.Fatal(err)
			}
			if v.Status() != VideoStopped {
				t.Fatalf("stub status = %s", v.Status())
			}
			if err := v.Play(); err != nil {
				t.Fatal(err)
			}
			if err := v.Prepare(); err != nil {
				t.Fatal(err)
			}
			if err := v.Seek(0); err != nil {
				t.Fatal(err)
			}
			if err := v.SetFrame(0); err != nil {
				t.Fatal(err)
			}
			if _, ok := v.Source(); ok {
				t.Fatal("empty Windows Media stub installed source metadata")
			}
			if _, ok := v.CurrentFrame(); ok {
				t.Fatal("empty Windows Media stub presented a frame")
			}
		})
	}
}
func TestWindowsMediaStreamCodecDiagnostics(t *testing.T) {
	const (
		headerGUID           = "\x30\x26\xb2\x75\x8e\x66\xcf\x11\xa6\xd9\x00\xaa\x00\x62\xce\x6c"
		streamPropertiesGUID = "\x91\x07\xdc\xb7\xb7\xa9\xcf\x11\x8e\xe6\x00\xc0\x0c\x20\x53\x65"
		videoMediaGUID       = "\xc0\xef\x19\xbc\x4d\x5b\xcf\x11\xa8\xfd\x00\x80\x5f\x5c\x44\x2b"
	)
	objectSize := 78 + 31
	data := make([]byte, 30+objectSize)
	copy(data, headerGUID)
	binary.LittleEndian.PutUint64(data[16:24], uint64(len(data)))
	binary.LittleEndian.PutUint32(data[24:28], 1)
	copy(data[30:46], streamPropertiesGUID)
	binary.LittleEndian.PutUint64(data[46:54], uint64(objectSize))
	copy(data[54:70], videoMediaGUID)
	binary.LittleEndian.PutUint32(data[94:98], 31)
	binary.LittleEndian.PutUint16(data[102:104], 2)
	copy(data[30+78+27:30+78+31], "WMV3")
	got := windowsMediaStreamCodecs(data)
	if len(got) != 1 || got[0] != "video stream 2 codec WMV3" {
		t.Fatalf("ASF stream codec diagnostics = %v, want video stream 2 codec WMV3", got)
	}
	video := NewVideoOverlay()
	if err := video.Open("clip.asf", data); err != nil {
		t.Fatal(err)
	}
	if video.Status() != VideoStopped {
		t.Fatalf("stub status = %s", video.Status())
	}
	if _, ok := video.Source(); ok {
		t.Fatal("empty ASF stub installed source metadata")
	}
}

func TestVideoMPEGMalformedPreservesSource(t *testing.T) {
	// An invalid sequence marker is rejected before any source state is installed.
	data := append([]byte{0, 0, 1, 0xb3, 0x01, 0x00, 0x10, 0x32, 0, 0, 0, 0}, []byte{0, 0, 1, 0, 0, 0, 0x08}...)
	v := NewVideoOverlay()
	err := v.Open("movies/intro.mpg", data)
	if !errors.Is(err, assets.ErrMPEGMalformed) {
		t.Fatalf("error = %v", err)
	}
	var ve *VideoError
	if !errors.As(err, &ve) || ve.Source != "movies/intro.mpg" || !strings.Contains(err.Error(), "intro.mpg") {
		t.Fatalf("source context = %v", err)
	}
	if _, ok := v.Source(); ok {
		t.Fatal("unsupported source was installed")
	}
}

func runtimeMPEGFixture() []byte {
	sequence := []byte{0, 0, 1, 0xb3, 0x01, 0x00, 0x10, 0x32, 0, 0, 0x20, 0}
	picture := []byte{0, 0, 1, 0, 0, 0x08, 0, 0}
	slice := []byte{0, 0, 1, 0x01, 0x0b, 0x94, 0xa5, 0x22, 0x20}
	pesPayload := append(append(sequence, picture...), slice...)
	pts := uint64(90_000)
	stamp := [5]byte{byte(0x21 | ((pts >> 30) & 7 << 1)), byte(pts >> 22), byte(((pts>>15)&0x7f)<<1 | 1), byte(pts >> 7), byte((pts&0x7f)<<1 | 1)}
	body := append([]byte{0x80, 0x80, 5}, stamp[:]...)
	body = append(body, pesPayload...)
	pes := []byte{0, 0, 1, 0xe0, 0, 0}
	binary.BigEndian.PutUint16(pes[4:], uint16(len(body)))
	pes = append(pes, body...)
	packet := make([]byte, 188)
	packet[0], packet[1], packet[2], packet[3] = 0x47, 0x40, 0x01, 0x10
	copy(packet[4:], pes)
	for i := range packet[4+len(pes):] {
		packet[4+len(pes)+i] = 0xff
	}
	return packet
}

func TestVideoMPEGOpenCurrentFrameAndLayer(t *testing.T) {
	v := NewVideoOverlay()
	if err := v.Open("movies/intro.mpg", runtimeMPEGFixture()); err != nil {
		t.Fatal(err)
	}
	source, ok := v.Source()
	if !ok || source.Format != "MPEG" || source.Width != 16 || source.Height != 16 || source.Frames != 1 {
		t.Fatalf("source = %+v, %v", source, ok)
	}
	frame, ok := v.CurrentFrame()
	if !ok || frame.Index != 0 || frame.Width != 16 || frame.Height != 16 || frame.PTSMS != 1000 || len(frame.RGBA) != 16*16*4 {
		t.Fatalf("frame = %+v, %v", frame, ok)
	}
	if frame.RGBA[0] != 130 || frame.RGBA[1] != 130 || frame.RGBA[2] != 130 || frame.RGBA[3] != 255 {
		t.Fatalf("neutral MPEG pixel = %v", frame.RGBA[:4])
	}
	layer := NewLayer(17)
	if err := v.SetLayer(0, layer); err != nil {
		t.Fatal(err)
	}
	if err := v.SetFrame(0); err != nil {
		t.Fatal(err)
	}
	if len(layer.RGBA) != len(frame.RGBA) || layer.RGBA[0] != 130 || layer.RGBA[3] != 255 {
		t.Fatalf("layer MPEG pixels = %v", layer.RGBA[:4])
	}
}

func TestVideoMPEGUnsupportedWithoutIFrame(t *testing.T) {
	data := append([]byte{0, 0, 1, 0xb3, 0x01, 0x00, 0x10, 0x32, 0, 0, 0x20, 0}, []byte{0, 0, 1, 0xb7}...)
	v := NewVideoOverlay()
	err := v.Open("movies/empty.mpg", data)
	if !errors.Is(err, ErrVideoUnsupported) {
		t.Fatalf("error = %v", err)
	}
	if _, ok := v.Source(); ok {
		t.Fatal("unsupported MPEG source was installed")
	}
}

func runtimeAMV(entropies [][]byte, alphas []byte) []byte {
	const headerSize = 40 + 2*64
	b := make([]byte, headerSize)
	copy(b, []byte("AJPM"))
	binary.LittleEndian.PutUint32(b[8:], 0)
	binary.LittleEndian.PutUint32(b[12:], headerSize)
	binary.LittleEndian.PutUint32(b[20:], uint32(len(entropies)))
	binary.LittleEndian.PutUint32(b[24:], 1)
	binary.LittleEndian.PutUint32(b[28:], 2)
	binary.LittleEndian.PutUint16(b[32:], 16)
	binary.LittleEndian.PutUint16(b[34:], 16)
	b[36] = 2 // deflated alpha
	for i := 40; i < headerSize; i++ {
		b[i] = 1
	}
	for i, entropy := range entropies {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		alpha := bytes.Repeat([]byte{alphas[i]}, 16*16)
		_, _ = zw.Write(alpha)
		_ = zw.Close()
		b = append(b, []byte("FRAM")...)
		packetLength := 16 + compressed.Len() + len(entropy)
		var x [4]byte
		binary.LittleEndian.PutUint32(x[:], uint32(packetLength))
		b = append(b, x[:]...)
		base := len(b)
		b = append(b, make([]byte, 16)...)
		binary.LittleEndian.PutUint32(b[base:], uint32(i))
		binary.LittleEndian.PutUint16(b[base+8:], 16)
		binary.LittleEndian.PutUint16(b[base+10:], 16)
		binary.LittleEndian.PutUint32(b[base+12:], uint32(compressed.Len()))
		b = append(b, compressed.Bytes()...)
		b = append(b, entropy...)
	}
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)))
	return b
}

func runtimeAMVZeroEntropy() []byte {
	var out []byte
	var current byte
	used := 0
	put := func(value byte, width int) {
		for bit := width - 1; bit >= 0; bit-- {
			if value&(1<<uint(bit)) != 0 {
				current |= 1 << uint(7-used)
			}
			used++
			if used == 8 {
				out = append(out, current)
				current, used = 0, 0
			}
		}
	}
	put(0, 2)
	put(0, 2)
	put(0, 2)
	put(0, 2)
	for range 8 {
		put(0, 2)
		put(0xA, 4)
	}
	if used != 0 {
		out = append(out, current)
	}
	return out
}

func TestVideoOverlayLayerPropertiesBindRuntimeLayers(t *testing.T) {
	session := NewSession(DefaultServices(), 100_000)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.executeSource("<video-layer-native-test>", `
		video = new VideoOverlay();
		window = new Window();
		layer = new Layer(window, null);
		video.layer1 = layer;
		attached = video.layer1 === layer;
		video.layer1 = null;
		detached = video.layer1 === null;
	`, false); err != nil {
		t.Fatal(err)
	}
	value, err := session.Evaluate("attached && detached")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := value.Truth()
	if err != nil || !ok {
		t.Fatalf("VideoOverlay layer1 property state = %v, err=%v", value, err)
	}
}

func TestVideoAMVInitialSeekAndLayer(t *testing.T) {
	entropy := runtimeAMVZeroEntropy()
	data := runtimeAMV([][]byte{entropy, entropy}, []byte{17, 231})
	v := NewVideoOverlay()
	if err := v.Open("movies/alpha.amv", data); err != nil {
		t.Fatal(err)
	}
	data[0] = 0 // Open owns its bounded source representation.
	source, ok := v.Source()
	if !ok || source.Format != "AMV" || source.Frames != 2 {
		t.Fatalf("source = %+v, %v", source, ok)
	}
	frame, ok := v.CurrentFrame()
	if !ok || frame.Index != 0 || frame.Width != 16 || frame.RGBA[3] != 17 {
		t.Fatalf("initial frame = %+v, %v", frame, ok)
	}
	layer := NewLayer(9)
	if err := v.SetLayer(0, layer); err != nil {
		t.Fatal(err)
	}
	if layer.RGBA[3] != 17 {
		t.Fatalf("initial layer alpha = %d", layer.RGBA[3])
	}
	if err := v.SetFrame(1); err != nil {
		t.Fatal(err)
	}
	frame, ok = v.CurrentFrame()
	if !ok || frame.Index != 1 || frame.RGBA[3] != 231 || layer.RGBA[3] != 231 {
		t.Fatalf("seek frame = %+v, layer alpha=%d, ok=%v", frame, layer.RGBA[3], ok)
	}
}

func TestVideoAMVClockLoop(t *testing.T) {
	entropy := runtimeAMVZeroEntropy()
	v := NewVideoOverlay()
	if err := v.Open("movies/loop.amv", runtimeAMV([][]byte{entropy, entropy}, []byte{17, 231})); err != nil {
		t.Fatal(err)
	}
	v.Events()
	v.SetLoop(true)
	if err := v.Play(); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(0); err != nil {
		t.Fatal(err)
	}
	if err := v.Advance(500); err != nil {
		t.Fatal(err)
	}
	if v.Frame() != 1 {
		t.Fatalf("frame at half duration = %d", v.Frame())
	}
	if err := v.Advance(1000); err != nil {
		t.Fatal(err)
	}
	if v.Status() != VideoPlaying || v.Position() != 0 || v.Frame() != 0 {
		t.Fatalf("loop state status=%q position=%d frame=%d", v.Status(), v.Position(), v.Frame())
	}
	foundPeriod := false
	for _, event := range v.Events() {
		if event.Kind == "period" && event.Reason == 0 {
			foundPeriod = true
		}
	}
	if !foundPeriod {
		t.Fatal("loop did not emit period event")
	}
}

func TestVideoAMVMalformedPacketContext(t *testing.T) {
	data := runtimeAMV([][]byte{runtimeAMVZeroEntropy(), nil}, []byte{17, 231})
	v := NewVideoOverlay()
	if err := v.Open("movies/bad.amv", data); err != nil {
		t.Fatal(err)
	}
	err := v.SetFrame(1)
	if !errors.Is(err, assets.ErrAMVTruncated) {
		t.Fatalf("error = %v", err)
	}
	var ve *VideoError
	if !errors.As(err, &ve) || ve.Source != "movies/bad.amv" || !strings.Contains(err.Error(), "bad.amv") {
		t.Fatalf("source context = %v", err)
	}
}
