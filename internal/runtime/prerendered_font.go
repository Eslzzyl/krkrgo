package runtime

import (
	"encoding/binary"
	"fmt"
	"image"
	"reflect"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	prerenderedFontHeaderSize = 36
	prerenderedFontItemSize   = 20
	prerenderedFontMaxGlyph   = 16 << 20
)

var prerenderedFontMagic = []byte("TVP pre-rendered font\x1a")

type prerenderedFontItem struct {
	offset           uint32
	width, height    uint16
	originX, originY int16
	incX, incY, inc  int16
}

type prerenderedFontGlyph struct {
	item  prerenderedFontItem
	alpha *image.Alpha
}

type prerenderedFont struct {
	version uint8
	data    []byte
	items   map[rune]prerenderedFontItem
	mu      sync.Mutex
	cache   map[rune]*prerenderedFontGlyph
}

func parsePrerenderedFont(data []byte) (*prerenderedFont, error) {
	if len(data) < prerenderedFontHeaderSize || string(data[:len(prerenderedFontMagic)]) != string(prerenderedFontMagic) {
		return nil, fmt.Errorf("%w: invalid pre-rendered font header", ErrInvalidFont)
	}
	version := data[22]
	if version > 1 {
		return nil, fmt.Errorf("%w: unsupported pre-rendered font version %d", ErrInvalidFont, version)
	}
	if data[23] != 2 {
		return nil, fmt.Errorf("%w: pre-rendered font character index is not UTF-16", ErrInvalidFont)
	}
	count := uint64(binary.LittleEndian.Uint32(data[24:28]))
	indexOffset := uint64(binary.LittleEndian.Uint32(data[28:32]))
	itemsOffset := uint64(binary.LittleEndian.Uint32(data[32:36]))
	if count > 1<<16 || indexOffset < prerenderedFontHeaderSize || itemsOffset < prerenderedFontHeaderSize ||
		indexOffset+count*2 > uint64(len(data)) || itemsOffset+count*prerenderedFontItemSize > uint64(len(data)) {
		return nil, fmt.Errorf("%w: pre-rendered font index exceeds file size", ErrInvalidFont)
	}
	items := make(map[rune]prerenderedFontItem, count)
	var last rune = -1
	for i := uint64(0); i < count; i++ {
		character := rune(binary.LittleEndian.Uint16(data[indexOffset+i*2 : indexOffset+i*2+2]))
		if character <= last {
			return nil, fmt.Errorf("%w: pre-rendered font index is not strictly ordered", ErrInvalidFont)
		}
		last = character
		offset := itemsOffset + i*prerenderedFontItemSize
		itemData := data[offset : offset+prerenderedFontItemSize]
		item := prerenderedFontItem{
			offset:  binary.LittleEndian.Uint32(itemData[0:4]),
			width:   binary.LittleEndian.Uint16(itemData[4:6]),
			height:  binary.LittleEndian.Uint16(itemData[6:8]),
			originX: int16(binary.LittleEndian.Uint16(itemData[8:10])),
			originY: int16(binary.LittleEndian.Uint16(itemData[10:12])),
			incX:    int16(binary.LittleEndian.Uint16(itemData[12:14])),
			incY:    int16(binary.LittleEndian.Uint16(itemData[14:16])),
			inc:     int16(binary.LittleEndian.Uint16(itemData[16:18])),
		}
		pixels := uint64(item.width) * uint64(item.height)
		if pixels > prerenderedFontMaxGlyph || uint64(item.offset) > uint64(len(data)) {
			return nil, fmt.Errorf("%w: pre-rendered glyph U+%04X exceeds file limits", ErrInvalidFont, character)
		}
		items[character] = item
	}
	return &prerenderedFont{version: version, data: data, items: items, cache: make(map[rune]*prerenderedFontGlyph)}, nil
}

func (f *prerenderedFont) glyph(character rune) (*prerenderedFontGlyph, bool) {
	if f == nil {
		return nil, false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if cached, ok := f.cache[character]; ok {
		return cached, true
	}
	item, ok := f.items[character]
	if !ok {
		return nil, false
	}
	glyph := &prerenderedFontGlyph{item: item}
	if item.width != 0 && item.height != 0 {
		pixels := int(item.width) * int(item.height)
		source := int(item.offset)
		decoded := make([]byte, pixels)
		for out := 0; out < pixels; {
			if source >= len(f.data) {
				return nil, false
			}
			value := f.data[source]
			source++
			isRun := (f.version == 0 && value == 0x41) || (f.version >= 1 && value >= 0x41)
			if !isRun {
				decoded[out] = value
				out++
				continue
			}
			runLength := int(value) - 0x40
			if f.version == 0 {
				if source >= len(f.data) {
					return nil, false
				}
				runLength = int(f.data[source])
				source++
			}
			if out == 0 || runLength <= 0 || runLength > pixels-out {
				return nil, false
			}
			last := decoded[out-1]
			for range runLength {
				decoded[out] = last
				out++
			}
		}
		alpha := image.NewAlpha(image.Rect(0, 0, int(item.width), int(item.height)))
		for y := 0; y < int(item.height); y++ {
			sourceY := int(item.height) - 1 - y
			copy(alpha.Pix[y*alpha.Stride:y*alpha.Stride+int(item.width)], decoded[sourceY*int(item.width):(sourceY+1)*int(item.width)])
		}
		glyph.alpha = alpha
	}
	f.cache[character] = glyph
	return glyph, true
}

type prerenderedFontMapKey struct {
	face                               string
	height, angle                      int32
	bold, italic, underline, strikeout bool
}

func prerenderedFontMapKeyFor(state FontState) prerenderedFontMapKey {
	return prerenderedFontMapKey{
		face: state.Face, height: state.Height, angle: state.Angle,
		bold: state.Bold, italic: state.Italic, underline: state.Underline, strikeout: state.Strikeout,
	}
}

type prerenderedFontFace struct {
	FontFace
	font *prerenderedFont
}

func (f *prerenderedFontFace) Glyph(dot fixed.Point26_6, character rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	glyph, ok := f.font.glyph(character)
	if !ok {
		return f.FontFace.Glyph(dot, character)
	}
	item := glyph.item
	advance := fixed.I(int(item.incX))
	if glyph.alpha == nil {
		return image.Rectangle{}, nil, image.Point{}, advance, true
	}
	x := dot.X.Round() + int(item.originX)
	y := dot.Y.Round() - int(item.originY)
	return image.Rect(x, y, x+int(item.width), y+int(item.height)), glyph.alpha, image.Point{}, advance, true
}

func (f *prerenderedFontFace) GlyphAdvance(character rune) (fixed.Int26_6, bool) {
	if glyph, ok := f.font.glyph(character); ok {
		return fixed.I(int(glyph.item.incX)), true
	}
	return f.FontFace.GlyphAdvance(character)
}

func (f *prerenderedFontFace) Kern(left, right rune) fixed.Int26_6 {
	if _, ok := f.font.glyph(left); ok {
		if _, rightMapped := f.font.glyph(right); rightMapped {
			return 0
		}
	}
	return f.FontFace.Kern(left, right)
}

func (s *Services) loadPrerenderedFont(storage string) (*prerenderedFont, error) {
	s.ensureInit()
	key := fontKey(storage)
	s.mu.RLock()
	loaded := s.prerenderedFonts[key]
	s.mu.RUnlock()
	if loaded != nil {
		return loaded, nil
	}
	data, err := s.readStorage(storage)
	if err != nil {
		return nil, err
	}
	loaded, err = parsePrerenderedFont(data)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if existing := s.prerenderedFonts[key]; existing != nil {
		loaded = existing
	} else {
		s.prerenderedFonts[key] = loaded
	}
	s.mu.Unlock()
	return loaded, nil
}

type glyphFallbackChainFace struct {
	FontFace
	book      *FontBook
	requested string
	size      float64
	once      sync.Once
	fallbacks []FontFace
	mu        sync.RWMutex
	resolved  map[rune]FontFace
}

func withGlyphFallbackChain(primary FontFace, book *FontBook, requested string, size float64) FontFace {
	if primary == nil || book == nil {
		return withGlyphFallback(primary)
	}
	return &glyphFallbackChainFace{FontFace: primary, book: book, requested: requested, size: size, resolved: make(map[rune]FontFace)}
}

func (f *glyphFallbackChainFace) fallbackFaces() []FontFace {
	f.once.Do(func() {
		primaryKey := fontKey(f.requested)
		for _, name := range f.book.Names() {
			if fontKey(name) == primaryKey {
				continue
			}
			face, err := f.book.Select(name, f.size)
			if err == nil && face != nil {
				f.fallbacks = append(f.fallbacks, face)
			}
		}
	})
	return f.fallbacks
}

func (f *glyphFallbackChainFace) faceFor(character rune) (FontFace, rune, bool) {
	f.mu.RLock()
	if face := f.resolved[character]; face != nil {
		f.mu.RUnlock()
		return face, character, true
	}
	f.mu.RUnlock()
	if _, ok := f.FontFace.GlyphAdvance(character); ok {
		f.mu.Lock()
		f.resolved[character] = f.FontFace
		f.mu.Unlock()
		return f.FontFace, character, true
	}
	for _, candidate := range f.fallbackFaces() {
		if _, ok := candidate.GlyphAdvance(character); ok {
			f.mu.Lock()
			f.resolved[character] = candidate
			f.mu.Unlock()
			return candidate, character, true
		}
	}
	return nil, 0, false
}

func (f *glyphFallbackChainFace) Glyph(dot fixed.Point26_6, character rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	face, resolved, ok := f.faceFor(character)
	if !ok {
		return f.FontFace.Glyph(dot, character)
	}
	return face.Glyph(dot, resolved)
}

func (f *glyphFallbackChainFace) GlyphAdvance(character rune) (fixed.Int26_6, bool) {
	face, resolved, ok := f.faceFor(character)
	if !ok {
		return f.FontFace.GlyphAdvance(character)
	}
	return face.GlyphAdvance(resolved)
}

func (f *glyphFallbackChainFace) Kern(left, right rune) fixed.Int26_6 {
	leftFace, leftRune, leftOK := f.faceFor(left)
	rightFace, rightRune, rightOK := f.faceFor(right)
	if !leftOK || !rightOK || !sameFontFace(leftFace, rightFace) {
		return 0
	}
	return leftFace.Kern(leftRune, rightRune)
}

func sameFontFace(left, right FontFace) bool {
	if left == nil || right == nil {
		return false
	}
	leftType, rightType := reflect.TypeOf(left), reflect.TypeOf(right)
	return leftType == rightType && leftType.Comparable() && left == right
}

func (f *glyphFallbackChainFace) Close() error {
	var first error
	if err := f.FontFace.Close(); err != nil {
		first = err
	}
	f.once.Do(func() {})
	for _, face := range f.fallbacks {
		if err := face.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

var _ font.Face = (*prerenderedFontFace)(nil)
var _ font.Face = (*glyphFallbackChainFace)(nil)
