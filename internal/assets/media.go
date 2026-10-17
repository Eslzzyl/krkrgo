package assets

import (
	"bytes"
	"errors"
	_ "golang.org/x/image/webp"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
)

const (
	maxPixels      = uint64(64 * 1024 * 1024)
	maxImageWidth  = uint32(16384)
	maxImageHeight = uint32(16384)
)

// ErrUnsupported is returned when a recognized codec cannot be represented by
// the bounded media API. TLG5 is decoded by tlg.go.
var ErrUnsupported = errors.New("unsupported media codec")

// FormatError identifies a malformed or unsupported resource without losing
// the original detail in Error().
type FormatError struct {
	Kind error
	Msg  string
}

func (e *FormatError) Error() string { return e.Msg }
func (e *FormatError) Unwrap() error { return e.Kind }

func malformed(format, msg string) error {
	return &FormatError{Kind: ErrMalformed, Msg: format + ": " + msg}
}

func unsupported(format string) error {
	return &FormatError{Kind: ErrUnsupported, Msg: "unsupported " + format}
}

// DecodeImage decodes an image using the Go standard library and x/image codecs
// and returns non-premultiplied RGBA pixels. BMP is handled by the bounded
// decoder in media_indexed.go.
func DecodeImage(data []byte) (Image, error) {
	if bytes.HasPrefix(data, []byte("TLG")) {
		return DecodeTLG(data)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if bytes.HasPrefix(data, []byte("BM")) {
			return decodeBMPImage(data)
		}
		return Image{}, &FormatError{Kind: ErrMalformed, Msg: "decode image: " + err.Error()}
	}
	if err := checkDimensions(uint32(cfg.Width), uint32(cfg.Height)); err != nil {
		return Image{}, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, &FormatError{Kind: ErrMalformed, Msg: "decode " + format + " image: " + err.Error()}
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if err := checkDimensions(uint32(width), uint32(height)); err != nil {
		return Image{}, err
	}
	rgba := imageToStraightRGBA(decoded, bounds)
	return Image{Width: uint32(width), Height: uint32(height), RGBA: rgba.Pix}, nil
}

// imageToStraightRGBA converts a decoded image to the straight-alpha RGBA
// representation used by Image, explicitly unpremultiplying premultiplied
// formats with rounded integer math and normalizing transparent pixels.
func imageToStraightRGBA(decoded image.Image, bounds image.Rectangle) *image.NRGBA {
	if src, ok := decoded.(*image.NRGBA); ok &&
		bounds.Min.X == 0 && bounds.Min.Y == 0 && src.Stride == bounds.Dx()*4 {
		clearTransparentRGBA(src.Pix)
		return src
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	switch src := decoded.(type) {
	case *image.NRGBA:
		for y := range bounds.Dy() {
			srcOffset := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
			dstOffset := y * rgba.Stride
			copy(rgba.Pix[dstOffset:dstOffset+bounds.Dx()*4], src.Pix[srcOffset:srcOffset+bounds.Dx()*4])
		}
		clearTransparentRGBA(rgba.Pix)
	case *image.RGBA:
		for y := range bounds.Dy() {
			srcOffset := src.PixOffset(bounds.Min.X, bounds.Min.Y+y)
			dstOffset := y * rgba.Stride
			for range bounds.Dx() {
				alpha := src.Pix[srcOffset+3]
				switch alpha {
				case 0:
					rgba.Pix[dstOffset], rgba.Pix[dstOffset+1], rgba.Pix[dstOffset+2] = 0, 0, 0
				case 255:
					copy(rgba.Pix[dstOffset:dstOffset+3], src.Pix[srcOffset:srcOffset+3])
				default:
					rgba.Pix[dstOffset] = unpremultiplyRGBAChannel(uint32(src.Pix[srcOffset]), uint32(alpha))
					rgba.Pix[dstOffset+1] = unpremultiplyRGBAChannel(uint32(src.Pix[srcOffset+1]), uint32(alpha))
					rgba.Pix[dstOffset+2] = unpremultiplyRGBAChannel(uint32(src.Pix[srcOffset+2]), uint32(alpha))
				}
				rgba.Pix[dstOffset+3] = alpha
				srcOffset += 4
				dstOffset += 4
			}
		}
	case *image.RGBA64:
		for y := range bounds.Dy() {
			dstOffset := y * rgba.Stride
			for x := range bounds.Dx() {
				pixel := src.RGBA64At(bounds.Min.X+x, bounds.Min.Y+y)
				alpha := uint32(pixel.A)
				alpha8 := byte(alpha >> 8)
				if alpha == 0 || alpha8 == 0 {
					rgba.Pix[dstOffset], rgba.Pix[dstOffset+1], rgba.Pix[dstOffset+2] = 0, 0, 0
				} else if alpha == 0xffff {
					rgba.Pix[dstOffset] = byte(pixel.R >> 8)
					rgba.Pix[dstOffset+1] = byte(pixel.G >> 8)
					rgba.Pix[dstOffset+2] = byte(pixel.B >> 8)
				} else {
					rgba.Pix[dstOffset] = unpremultiplyRGBAChannel(uint32(pixel.R), alpha)
					rgba.Pix[dstOffset+1] = unpremultiplyRGBAChannel(uint32(pixel.G), alpha)
					rgba.Pix[dstOffset+2] = unpremultiplyRGBAChannel(uint32(pixel.B), alpha)
				}
				rgba.Pix[dstOffset+3] = alpha8
				dstOffset += 4
			}
		}
	case *image.NRGBA64:
		for y := range bounds.Dy() {
			dstOffset := y * rgba.Stride
			for x := range bounds.Dx() {
				pixel := src.NRGBA64At(bounds.Min.X+x, bounds.Min.Y+y)
				alpha := byte(pixel.A >> 8)
				if alpha == 0 {
					rgba.Pix[dstOffset], rgba.Pix[dstOffset+1], rgba.Pix[dstOffset+2] = 0, 0, 0
				} else {
					rgba.Pix[dstOffset] = byte(pixel.R >> 8)
					rgba.Pix[dstOffset+1] = byte(pixel.G >> 8)
					rgba.Pix[dstOffset+2] = byte(pixel.B >> 8)
				}
				rgba.Pix[dstOffset+3] = alpha
				dstOffset += 4
			}
		}
	default:
		draw.Draw(rgba, rgba.Bounds(), decoded, bounds.Min, draw.Src)
		clearTransparentRGBA(rgba.Pix)
	}
	return rgba
}

func unpremultiplyRGBAChannel(channel, alpha uint32) byte {
	if alpha == 0 {
		return 0
	}
	value := (channel*255 + alpha/2) / alpha
	if value > 255 {
		value = 255
	}
	return byte(value)
}

func clearTransparentRGBA(pixels []byte) {
	for pixel := range len(pixels) / 4 {
		offset := pixel * 4
		if pixels[offset+3] == 0 {
			pixels[offset], pixels[offset+1], pixels[offset+2] = 0, 0, 0
		}
	}
}

// Metadata is also available as a method-shaped adapter for code translating
// the Rust Image::metadata associated function.
func (Image) Metadata(data []byte) (map[string]string, error) {
	return ImageMetadata(data)
}

// Decode is also available as a method-shaped adapter for Image::decode.
func (Image) Decode(data []byte) (Image, error) { return DecodeImage(data) }

// WritePNG writes an image as PNG and refuses to overwrite an existing file.
func (im Image) WritePNG(path string) error {
	if !validImageSize(im.Width, im.Height, len(im.RGBA)) {
		return malformed("image", "invalid RGBA image size")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	encoded := &image.NRGBA{Pix: im.RGBA, Stride: int(im.Width) * 4, Rect: image.Rect(0, 0, int(im.Width), int(im.Height))}
	if err := png.Encode(file, encoded); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

// WritePNG writes an Image as PNG without allowing replacement of an existing
// destination.
func WritePNG(im Image, path string) error { return im.WritePNG(path) }

func validImageSize(width, height uint32, n int) bool {
	if width == 0 || height == 0 || width > maxImageWidth || height > maxImageHeight {
		return false
	}
	return uint64(width)*uint64(height) <= maxPixels && uint64(n) == uint64(width)*uint64(height)*4
}

func checkDimensions(width, height uint32) error {
	if width == 0 || height == 0 || width > maxImageWidth || height > maxImageHeight || uint64(width)*uint64(height) > maxPixels {
		return malformed("image", "invalid dimensions")
	}
	return nil
}

// Audio contains interleaved signed 16-bit PCM samples.
type Audio struct {
	Channels   uint8
	SampleRate uint32
	Samples    []int16
}

// Frames returns the number of interleaved PCM frames.
func (a Audio) Frames() int {
	if a.Channels == 0 {
		return 0
	}
	return len(a.Samples) / int(a.Channels)
}

// DecodeWave and the RIFF/WAVE parser are provided by the FFmpeg sidecar
// (internal/media/ffmpeg); Audio remains the runtime PCM representation.
