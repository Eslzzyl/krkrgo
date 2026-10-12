package assets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"

)

const (
	maxPixels        = uint64(64 * 1024 * 1024)
	maxImageWidth    = uint32(16384)
	maxImageHeight   = uint32(16384)
	maxAudioChannels = uint16(8)
	maxSampleRate    = uint32(768000)
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

// DecodeImage decodes an image using the codecs included in the Go standard
// library and returns non-premultiplied RGBA pixels. BMP is handled by the
// bounded decoder in media_indexed.go.
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
	rgba := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	switch src := decoded.(type) {
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

// DecodeWave decodes RIFF/WAVE data into the bounded signed 16-bit Audio
// representation. IEEE float and WAVE_FORMAT_EXTENSIBLE PCM/float sources are
// converted to int16. Audio intentionally omits source metadata (speaker mask,
// total samples/time, and seekability), so those C++ WaveIntf fields remain unavailable.
func DecodeWave(data []byte, maxFrames int) (Audio, error) {
	if maxFrames < 0 {
		return Audio{}, malformed("WAVE", "negative frame limit")
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return Audio{}, malformed("WAVE", "invalid RIFF header")
	}
	riffSize := uint64(binary.LittleEndian.Uint32(data[4:8])) + 8
	if riffSize < 12 || riffSize > uint64(len(data)) {
		return Audio{}, malformed("WAVE", "invalid RIFF size")
	}
	end := int(riffSize)
	var format waveFormat
	var pcm []byte
	for pos := 12; pos < end; {
		if end-pos < 8 {
			return Audio{}, malformed("WAVE", "truncated chunk header")
		}
		tag := data[pos : pos+4]
		size := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if size > uint64(end-pos) {
			return Audio{}, malformed("WAVE", "truncated chunk")
		}
		body := data[pos : pos+int(size)]
		pos += int(size)
		if size&1 != 0 {
			if pos >= end {
				return Audio{}, malformed("WAVE", "missing chunk padding")
			}
			pos++
		}
		switch string(tag) {
		case "fmt ":
			if format.present {
				return Audio{}, malformed("WAVE", "duplicate format chunk")
			}
			parsed, err := parseWaveFormat(body)
			if err != nil {
				return Audio{}, err
			}
			format = parsed
		case "data":
			if pcm != nil {
				return Audio{}, malformed("WAVE", "duplicate data chunk")
			}
			pcm = body
		}
	}
	if !format.present {
		return Audio{}, malformed("WAVE", "format chunk missing")
	}
	if pcm == nil {
		return Audio{}, malformed("WAVE", "data chunk missing")
	}
	if len(pcm)%int(format.blockAlign) != 0 {
		return Audio{}, malformed("WAVE", "partial PCM frame")
	}
	frames := len(pcm) / int(format.blockAlign)
	if frames > maxFrames {
		return Audio{}, malformed("WAVE", "decoded audio exceeds frame limit")
	}
	if frames > 0 && uint64(frames)*uint64(format.channels) > uint64(^uint(0)>>1) {
		return Audio{}, malformed("WAVE", "audio sample count overflow")
	}
	samples := make([]int16, frames*int(format.channels))
	step := int(format.bytesPerSample)
	for i := range samples {
		off := (i/int(format.channels))*int(format.blockAlign) + (i%int(format.channels))*step
		if format.isFloat {
			samples[i] = waveFloat16(math.Float32frombits(binary.LittleEndian.Uint32(pcm[off : off+4])))
		} else {
			samples[i] = waveInteger16(pcm[off:off+step], format.bytesPerSample, format.validBits)
		}
	}
	return Audio{Channels: uint8(format.channels), SampleRate: format.sampleRate, Samples: samples}, nil
}

type waveFormat struct {
	present        bool
	channels       uint16
	sampleRate     uint32
	blockAlign     uint16
	bytesPerSample uint16
	validBits      uint16
	isFloat        bool
}

const (
	waveFormatPCM        = 1
	waveFormatIEEEFloat  = 3
	waveFormatExtensible = 0xfffe
)

var (
	wavePCMSubFormat   = [16]byte{1, 0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113}
	waveFloatSubFormat = [16]byte{3, 0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113}
)

func parseWaveFormat(body []byte) (waveFormat, error) {
	if len(body) < 16 {
		return waveFormat{}, malformed("WAVE", "invalid format chunk")
	}
	tag := binary.LittleEndian.Uint16(body[:2])
	channels := binary.LittleEndian.Uint16(body[2:4])
	rate := binary.LittleEndian.Uint32(body[4:8])
	blockAlign := binary.LittleEndian.Uint16(body[12:14])
	containerBits := binary.LittleEndian.Uint16(body[14:16])
	if tag != waveFormatPCM && tag != waveFormatIEEEFloat && tag != waveFormatExtensible {
		return waveFormat{}, unsupported("non-PCM WAVE")
	}
	if channels == 0 || channels > maxAudioChannels || rate == 0 || rate > maxSampleRate {
		return waveFormat{}, malformed("WAVE", "invalid channel count or sample rate")
	}
	if containerBits < 8 || containerBits > 32 || containerBits%8 != 0 {
		return waveFormat{}, unsupported("WAVE PCM bit depth")
	}
	bytesPerSample := containerBits / 8
	validBits := containerBits
	isFloat := tag == waveFormatIEEEFloat
	if tag == waveFormatExtensible {
		if len(body) < 18 {
			return waveFormat{}, malformed("WAVE", "truncated extensible format")
		}
		cbSize := binary.LittleEndian.Uint16(body[16:18])
		if cbSize != 22 || len(body) < 16+2+int(cbSize) {
			return waveFormat{}, malformed("WAVE", "invalid extensible format")
		}
		validBits = binary.LittleEndian.Uint16(body[18:20])
		if validBits < 8 || validBits > containerBits {
			return waveFormat{}, malformed("WAVE", "invalid valid bit depth")
		}
		subFormat := body[24:40]
		switch {
		case bytes.Equal(subFormat, wavePCMSubFormat[:]):
			isFloat = false
		case bytes.Equal(subFormat, waveFloatSubFormat[:]):
			isFloat = true
		default:
			return waveFormat{}, unsupported("WAVE extensible subformat")
		}
	}
	if isFloat && (containerBits != 32 || validBits != 32 || bytesPerSample != 4) {
		return waveFormat{}, unsupported("WAVE IEEE float bit depth")
	}
	if blockAlign != channels*bytesPerSample {
		return waveFormat{}, malformed("WAVE", "invalid block alignment")
	}
	return waveFormat{
		present: true, channels: channels, sampleRate: rate, blockAlign: blockAlign,
		bytesPerSample: bytesPerSample, validBits: validBits, isFloat: isFloat,
	}, nil
}

func waveInteger16(sample []byte, bytesPerSample, validBits uint16) int16 {
	switch bytesPerSample {
	case 1:
		return int16((int(sample[0]) - 128) << 8)
	case 2:
		raw := binary.LittleEndian.Uint16(sample)
		if validBits < 16 {
			raw &= ^uint16((uint32(1) << (16 - validBits)) - 1)
		}
		return int16(raw)
	case 3:
		v := int32(sample[0]) | int32(sample[1])<<8 | int32(sample[2])<<16
		if v&0x800000 != 0 {
			v |= ^0xffffff
		}
		if validBits < 24 {
			v &= int32(^uint32((uint32(1) << (24 - validBits)) - 1))
		}
		return int16(v >> 8)
	case 4:
		raw := binary.LittleEndian.Uint32(sample)
		if validBits < 32 {
			raw &= ^uint32((uint64(1) << (32 - validBits)) - 1)
		}
		return int16(int32(raw) >> 16)
	default:
		return 0
	}
}

func waveFloat16(sample float32) int16 {
	if math.IsNaN(float64(sample)) {
		return 0
	}
	scaled := sample * 32767
	if scaled > 32767 {
		return 32767
	}
	if scaled < -32768 {
		return -32768
	}
	if scaled < 0 {
		return int16(scaled - 0.5)
	}
	return int16(scaled + 0.5)
}


// ReadAudio dispatches the bounded PCM decoder.
func ReadAudio(data []byte, maxFrames int) (Audio, error) {
	if bytes.HasPrefix(data, []byte("RIFF")) {
		return DecodeWave(data, maxFrames)
	}
	return Audio{}, unsupported("audio format")
}

// DecodeWave is a method-shaped adapter for Audio::decode_wave.
func (Audio) DecodeWave(data []byte, maxFrames int) (Audio, error) {
	return DecodeWave(data, maxFrames)
}

// The implementation intentionally accepts a byte slice rather than an open
// stream so callers cannot accidentally bypass the decoder's bounds checks.
