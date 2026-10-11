package assets

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
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

func TestDecodeVorbisAndReadAudio(t *testing.T) {
	data, err := testVorbisFixture()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeVorbis(data, 44100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Channels != 1 || got.SampleRate != 44100 {
		t.Fatalf("Vorbis format = channels %d, rate %d", got.Channels, got.SampleRate)
	}
	if len(got.Samples) != 44100 {
		t.Fatalf("Vorbis sample count = %d", len(got.Samples))
	}
	if !equalInt16(got.Samples[:4], []int16{189, 204, 257, 353}) {
		t.Fatalf("Vorbis samples = %v", got.Samples[:4])
	}
	viaDispatch, err := ReadAudio(data, 44100)
	if err != nil || !equalInt16(viaDispatch.Samples, got.Samples) {
		t.Fatalf("ReadAudio Vorbis = %v, err=%v", viaDispatch, err)
	}
	if _, err := DecodeVorbis(data, 3); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Vorbis frame limit error = %v", err)
	}
	if _, err := DecodeVorbis(data[:len(data)-1], 44100); !errors.Is(err, ErrMalformed) {
		t.Fatalf("truncated Vorbis error = %v", err)
	}
}

func TestUnsupportedCodecs(t *testing.T) {
	if _, err := DecodeImage([]byte("TLG6.0\x00raw\x1a")); !errors.Is(err, ErrTruncated) {
		t.Fatalf("TLG error = %v", err)
	}
	if _, err := DecodeVorbis([]byte("OggS"), 1); !errors.Is(err, ErrMalformed) {
		t.Fatalf("malformed Vorbis error = %v", err)
	}
}

func testVorbisFixture() ([]byte, error) {
	const compressed = `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`
	compressedData, err := base64.StdEncoding.DecodeString(compressed)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressedData))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if closeErr := zr.Close(); err == nil {
		err = closeErr
	}
	return data, err
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
