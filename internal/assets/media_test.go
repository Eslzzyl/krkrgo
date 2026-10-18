package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeTransitionRuleRGBA(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	img.SetRGBA(1, 0, color.RGBA{0, 0, 0, 255})
	img.SetRGBA(0, 1, color.RGBA{255, 0, 0, 255})
	img.SetRGBA(1, 1, color.RGBA{0, 255, 0, 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTransitionRuleImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{255, 0, 76, 150}
	if got.Width != 2 || got.Height != 2 || !bytes.Equal(got.Pixels, want) {
		t.Fatalf("transition rule = %dx%d %v, want 2x2 %v", got.Width, got.Height, got.Pixels, want)
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

func TestDecodeWebPImage(t *testing.T) {
	data := []byte{
		0x52, 0x49, 0x46, 0x46, 0x1e, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
		0x56, 0x50, 0x38, 0x4c, 0x11, 0x00, 0x00, 0x00, 0x2f, 0x01, 0x40, 0x00,
		0x00, 0x07, 0x50, 0x9a, 0x4a, 0xd4, 0xaa, 0xff, 0x81, 0x88, 0xe8, 0x7f,
		0x00, 0x00,
	}
	got, err := DecodeImage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 2 || got.Height != 2 {
		t.Fatalf("WebP dimensions = %dx%d, want 2x2", got.Width, got.Height)
	}
	if pixel := got.RGBA[:4]; !bytes.Equal(pixel, []byte{18, 52, 86, 255}) {
		t.Fatalf("WebP pixel = %v, want [18 52 86 255]", pixel)
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
