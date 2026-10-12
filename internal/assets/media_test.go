package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeWavePCMDepthsAndLimit(t *testing.T) {
	for _, tc := range []struct {
		bits uint16
		data []byte
		want []int16
	}{
		{8, []byte{0, 128, 255}, []int16{-32768, 0, 32512}},
		{16, []byte{0, 128, 0, 0, 255, 127}, []int16{-32768, 0, 32767}},
		{24, []byte{0, 0, 128, 255, 255, 255, 255, 255, 127}, []int16{-32768, -1, 32767}},
		{32, []byte{0, 0, 0, 128, 255, 255, 255, 255, 255, 255, 255, 127}, []int16{-32768, -1, 32767}},
	} {
		wave := testWave(tc.bits, 1, tc.data)
		got, err := DecodeWave(wave, 3)
		if err != nil {
			t.Fatalf("%d-bit wave: %v", tc.bits, err)
		}
		if !equalInt16(got.Samples, tc.want) || got.Frames() != 3 {
			t.Fatalf("%d-bit samples = %v, frames %d", tc.bits, got.Samples, got.Frames())
		}
		if _, err := DecodeWave(wave, 2); err == nil {
			t.Fatalf("%d-bit frame limit accepted", tc.bits)
		}
	}
}

func TestDecodeWaveIEEEFloat(t *testing.T) {
	input := []float32{-1, -0.5, 0, 0.5, 1, 2, -2, float32(math.NaN())}
	wave := testWaveFloat(input, 1)
	got, err := DecodeWave(wave, len(input))
	if err != nil {
		t.Fatal(err)
	}
	want := []int16{-32767, -16384, 0, 16384, 32767, 32767, -32768, 0}
	if !equalInt16(got.Samples, want) || got.Channels != 1 || got.SampleRate != 44100 {
		t.Fatalf("float samples=%v channels=%d rate=%d", got.Samples, got.Channels, got.SampleRate)
	}
	if _, err := DecodeWave(wave, len(input)-1); !errors.Is(err, ErrMalformed) {
		t.Fatalf("float frame limit error=%v", err)
	}
}

func TestDecodeWaveExtensiblePCMAndFloat(t *testing.T) {
	pcm := make([]byte, 4*4)
	for i, sample := range []int32{-8388608, 0, 8388607, -1} {
		binary.LittleEndian.PutUint32(pcm[i*4:], uint32(sample<<8))
	}
	wave := testWaveExtensible(32, 24, 2, wavePCMSubFormat[:], pcm)
	got, err := DecodeWave(wave, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt16(got.Samples, []int16{-32768, 0, 32767, -1}) || got.Frames() != 2 {
		t.Fatalf("extensible PCM samples=%v frames=%d", got.Samples, got.Frames())
	}

	floatWave := testWaveExtensible(32, 32, 2, waveFloatSubFormat[:], testFloatPCM([]float32{-1, 0.5, 1, -0.5}))
	got, err = DecodeWave(floatWave, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt16(got.Samples, []int16{-32767, 16384, 32767, -16384}) {
		t.Fatalf("extensible float samples=%v", got.Samples)
	}
}

func TestDecodeWaveExtensibleRejections(t *testing.T) {
	pcm := []byte{0, 0, 0, 0}
	unknown := bytes.Repeat([]byte{0}, 16)
	if _, err := DecodeWave(testWaveExtensible(32, 32, 1, unknown, pcm), 1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown extensible subtype error=%v", err)
	}
	short := testWaveExtensible(32, 32, 1, wavePCMSubFormat[:], pcm)
	// The extensible body starts at offset 20; retain the fmt chunk header but
	// truncate the body before its GUID while keeping the RIFF size consistent.
	short = append([]byte(nil), short[:20+24]...)
	binary.LittleEndian.PutUint32(short[4:8], uint32(len(short)-8))
	if _, err := DecodeWave(short, 1); !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated extensible format error=%v", err)
	}

	badFloat := testWaveFloat([]float32{0}, 1)
	binary.LittleEndian.PutUint16(badFloat[32:34], 2)
	binary.LittleEndian.PutUint16(badFloat[34:36], 16)
	if _, err := DecodeWave(badFloat, 1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("non-32-bit float error=%v", err)
	}
}

func TestDecodeIndexedPNGAndBMP(t *testing.T) {
	pal := color.Palette{color.RGBA{10, 20, 30, 255}, color.RGBA{200, 100, 50, 255}}
	indexed := image.NewPaletted(image.Rect(0, 0, 3, 2), pal)
	indexed.Pix = []byte{0, 1, 0, 1, 0, 1}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, indexed); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeIndexedImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 3 || got.Height != 2 || !bytes.Equal(got.Pixels, indexed.Pix) || len(got.Palette) != 2 {
		t.Fatalf("indexed result = %+v", got)
	}

	gray := image.NewGray(image.Rect(0, 0, 2, 1))
	gray.Pix = []byte{0, 255}
	encoded.Reset()
	if err := png.Encode(&encoded, gray); err != nil {
		t.Fatal(err)
	}
	got, err = DecodeIndexedImage(encoded.Bytes())
	if err != nil || got.Palette != nil || !bytes.Equal(got.Pixels, []byte{0, 255}) {
		t.Fatalf("grayscale result = %+v, err=%v", got, err)
	}

	bmp := make([]byte, 66)
	copy(bmp, "BM")
	binary.LittleEndian.PutUint32(bmp[2:6], uint32(len(bmp)))
	binary.LittleEndian.PutUint32(bmp[10:14], 62)
	binary.LittleEndian.PutUint32(bmp[14:18], 40)
	binary.LittleEndian.PutUint32(bmp[18:22], 2)
	binary.LittleEndian.PutUint32(bmp[22:26], 1)
	binary.LittleEndian.PutUint16(bmp[26:28], 1)
	binary.LittleEndian.PutUint16(bmp[28:30], 8)
	binary.LittleEndian.PutUint32(bmp[46:50], 2)
	bmp[54], bmp[55], bmp[56] = 30, 20, 10
	bmp[58], bmp[59], bmp[60] = 50, 100, 200
	bmp[62], bmp[63] = 1, 0
	bmpGot, err := DecodeIndexedImage(bmp)
	if err != nil || !bytes.Equal(bmpGot.Pixels, []byte{1, 0}) || len(bmpGot.Palette) != 2 {
		t.Fatalf("BMP result = %+v, err=%v", bmpGot, err)
	}
}

func TestImageToStraightRGBAUnpremultiplies(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 1))
	copy(src.Pix, []byte{1, 2, 3, 128, 0, 0, 0, 0, 10, 20, 30, 255})
	got := imageToStraightRGBA(src, src.Bounds())
	want := []byte{2, 4, 6, 128, 0, 0, 0, 0, 10, 20, 30, 255}
	if !bytes.Equal(got.Pix, want) {
		t.Fatalf("converted pixels = %v, want %v", got.Pix, want)
	}
}

func TestImageToStraightRGBA64(t *testing.T) {
	rect := image.Rect(0, 0, 3, 1)
	premultiplied := image.NewRGBA64(rect)
	premultiplied.SetRGBA64(0, 0, color.RGBA64{R: 257, G: 514, B: 771, A: 32896})
	premultiplied.SetRGBA64(1, 0, color.RGBA64{})
	premultiplied.SetRGBA64(2, 0, color.RGBA64{R: 2570, G: 5140, B: 7710, A: 0xffff})
	want := []byte{2, 4, 6, 128, 0, 0, 0, 0, 10, 20, 30, 255}
	if got := imageToStraightRGBA(premultiplied, rect); !bytes.Equal(got.Pix, want) {
		t.Fatalf("RGBA64 conversion = %v, want %v", got.Pix, want)
	}

	straight := image.NewNRGBA64(rect)
	straight.SetNRGBA64(0, 0, color.NRGBA64{R: 2570, G: 5140, B: 7710, A: 16448})
	straight.SetNRGBA64(1, 0, color.NRGBA64{R: 23130, G: 20560, B: 17990, A: 0})
	straight.SetNRGBA64(2, 0, color.NRGBA64{R: 2570, G: 5140, B: 7710, A: 0xffff})
	want = []byte{10, 20, 30, 64, 0, 0, 0, 0, 10, 20, 30, 255}
	if got := imageToStraightRGBA(straight, rect); !bytes.Equal(got.Pix, want) {
		t.Fatalf("NRGBA64 conversion = %v, want %v", got.Pix, want)
	}
}

func TestDecodeImageReturnsStraightRGBA(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	copy(src.Pix, []byte{10, 20, 30, 64, 90, 80, 70, 0, 1, 2, 3, 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{10, 20, 30, 64, 0, 0, 0, 0, 1, 2, 3, 255}
	if got.Width != 3 || got.Height != 1 || !bytes.Equal(got.RGBA, want) {
		t.Fatalf("decoded image = %dx%d %v, want 3x1 %v", got.Width, got.Height, got.RGBA, want)
	}
}

func TestImageWritePNGRoundTripsStraightRGBA(t *testing.T) {
	im := Image{Width: 1, Height: 1, RGBA: []byte{10, 20, 30, 64}}
	view := im.RGBAImage()
	if view == nil || !bytes.Equal(view.Pix, im.RGBA) {
		t.Fatalf("RGBAImage view = %v, want %v", view, im.RGBA)
	}
	path := filepath.Join(t.TempDir(), "image.png")
	if err := im.WritePNG(path); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeImage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.RGBA, im.RGBA) {
		t.Fatalf("PNG round-trip = %v, want %v", got.RGBA, im.RGBA)
	}
}

func TestImageMetadataAndWriteNoReplace(t *testing.T) {
	pngData := metadataPNGChunk("oFFs", []byte{0xff, 0xff, 0xff, 0xfe, 0, 0, 0, 7, 1})
	tags, err := ImageMetadata(pngData)
	if err != nil {
		t.Fatal(err)
	}
	if tags["offs_x"] != "-2" || tags["offs_y"] != "7" || tags["offs_unit"] != "micrometer" {
		t.Fatalf("metadata = %#v", tags)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "image.png")
	im := Image{Width: 1, Height: 1, RGBA: []byte{1, 2, 3, 255}}
	if err := im.WritePNG(path); err != nil {
		t.Fatal(err)
	}
	if err := im.WritePNG(path); err == nil {
		t.Fatal("WritePNG overwrote existing file")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}


func TestUnsupportedCodecs(t *testing.T) {
	if _, err := DecodeImage([]byte("TLG6.0\x00raw\x1a")); !errors.Is(err, ErrTruncated) {
		t.Fatalf("TLG error = %v", err)
	}
}


func testWave(bits, channels uint16, pcm []byte) []byte {
	block := channels * bits / 8
	pad := len(pcm) & 1
	out := make([]byte, 44+len(pcm)+pad)
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(36+len(pcm)+pad))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], channels)
	binary.LittleEndian.PutUint32(out[24:28], 44100)
	binary.LittleEndian.PutUint32(out[28:32], 44100*uint32(block))
	binary.LittleEndian.PutUint16(out[32:34], block)
	binary.LittleEndian.PutUint16(out[34:36], bits)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}

func testWaveFloat(samples []float32, channels uint16) []byte {
	return testWaveFormat(3, channels, testFloatPCM(samples))
}

func testFloatPCM(samples []float32) []byte {
	pcm := make([]byte, len(samples)*4)
	for i, sample := range samples {
		binary.LittleEndian.PutUint32(pcm[i*4:], math.Float32bits(sample))
	}
	return pcm
}

func testWaveFormat(tag, channels uint16, pcm []byte) []byte {
	bits := uint16(32)
	block := channels * bits / 8
	out := make([]byte, 44+len(pcm)+(len(pcm)&1))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], tag)
	binary.LittleEndian.PutUint16(out[22:24], channels)
	binary.LittleEndian.PutUint32(out[24:28], 44100)
	binary.LittleEndian.PutUint32(out[28:32], 44100*uint32(block))
	binary.LittleEndian.PutUint16(out[32:34], block)
	binary.LittleEndian.PutUint16(out[34:36], bits)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], uint32(len(pcm)))
	copy(out[44:], pcm)
	return out
}

func testWaveExtensible(containerBits, validBits, channels uint16, subFormat, pcm []byte) []byte {
	block := channels * containerBits / 8
	const fmtSize = 40
	out := make([]byte, 12+8+fmtSize+8+len(pcm)+(len(pcm)&1))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], fmtSize)
	binary.LittleEndian.PutUint16(out[20:22], waveFormatExtensible)
	binary.LittleEndian.PutUint16(out[22:24], channels)
	binary.LittleEndian.PutUint32(out[24:28], 44100)
	binary.LittleEndian.PutUint32(out[28:32], 44100*uint32(block))
	binary.LittleEndian.PutUint16(out[32:34], block)
	binary.LittleEndian.PutUint16(out[34:36], containerBits)
	binary.LittleEndian.PutUint16(out[36:38], 22)
	binary.LittleEndian.PutUint16(out[38:40], validBits)
	binary.LittleEndian.PutUint32(out[40:44], 0x3)
	copy(out[44:60], subFormat)
	copy(out[60:64], "data")
	binary.LittleEndian.PutUint32(out[64:68], uint32(len(pcm)))
	copy(out[68:], pcm)
	return out
}

func metadataPNGChunk(kind string, body []byte) []byte {
	var out bytes.Buffer
	out.Write([]byte("\x89PNG\r\n\x1a\n"))
	binary.Write(&out, binary.BigEndian, uint32(13))
	out.WriteString("IHDR")
	out.Write([]byte{0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0})
	binary.Write(&out, binary.BigEndian, crc32Bytes([]byte("IHDR"), out.Bytes()[16:29]))
	binary.Write(&out, binary.BigEndian, uint32(len(body)))
	out.WriteString(kind)
	out.Write(body)
	binary.Write(&out, binary.BigEndian, crc32Bytes([]byte(kind), body))
	binary.Write(&out, binary.BigEndian, uint32(0))
	out.WriteString("IEND")
	binary.Write(&out, binary.BigEndian, crc32Bytes([]byte("IEND"), nil))
	return out.Bytes()
}

func crc32Bytes(kind, body []byte) uint32 {
	var b bytes.Buffer
	b.Write(kind)
	b.Write(body)
	return crc32.ChecksumIEEE(b.Bytes())
}

func equalInt16(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
