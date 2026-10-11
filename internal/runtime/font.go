package runtime

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/text/unicode/norm"
)

// FontFace is the concrete, size-specific font interface used by the runtime.
// It intentionally aliases x/image/font.Face: callers can inject a deterministic
// face in tests, while production callers can provide a real OpenType face.
type FontFace = font.Face

// glyphFallbackFace mirrors FreeTypeFontRasterizer's missing-character policy:
// try the requested character, then the face's default character (space), then
// the first character the face can render. Keeping this at the FontFace
// boundary makes drawing, advance measurement, and kerning resolve identically.
type glyphFallbackFace struct {
	FontFace
	firstOnce sync.Once
	firstRune rune
	hasFirst  bool
}

func withGlyphFallback(face FontFace) FontFace {
	if face == nil {
		return nil
	}
	return &glyphFallbackFace{FontFace: face}
}

func (f *glyphFallbackFace) resolve(r rune) (rune, bool) {
	if _, ok := f.FontFace.GlyphAdvance(r); ok {
		return r, true
	}
	if r != ' ' {
		if _, ok := f.FontFace.GlyphAdvance(' '); ok {
			return ' ', true
		}
	}
	f.firstOnce.Do(func() {
		for candidate := rune(0); candidate <= 0x10ffff; candidate++ {
			if candidate >= 0xd800 && candidate <= 0xdfff {
				candidate = 0xdfff
				continue
			}
			if _, ok := f.FontFace.GlyphAdvance(candidate); ok {
				f.firstRune, f.hasFirst = candidate, true
				return
			}
		}
	})
	return f.firstRune, f.hasFirst
}

func (f *glyphFallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	resolved, ok := f.resolve(r)
	if !ok {
		return image.Rectangle{}, nil, image.Point{}, 0, false
	}
	return f.FontFace.Glyph(dot, resolved)
}

func (f *glyphFallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	resolved, ok := f.resolve(r)
	if !ok {
		return 0, false
	}
	return f.FontFace.GlyphAdvance(resolved)
}

func (f *glyphFallbackFace) Kern(left, right rune) fixed.Int26_6 {
	resolvedLeft, okLeft := f.resolve(left)
	resolvedRight, okRight := f.resolve(right)
	if !okLeft || !okRight {
		return 0
	}
	return f.FontFace.Kern(resolvedLeft, resolvedRight)
}

var (
	// ErrUnsupportedFont is returned when a requested font is not present in the
	// configured FontBook. Rendering never silently substitutes a fake glyph.
	ErrUnsupportedFont = errors.New("unsupported font")
	// ErrInvalidFont describes an invalid face, name, or font file.
	ErrInvalidFont = errors.New("invalid font")
)

// FontFactory creates a size-specific face. Factories are useful for OpenType
// fonts because one parsed file can provide many point sizes.
type FontFactory func(size float64) (FontFace, error)

type fontEntry struct {
	name       string
	face       FontFace
	factory    FontFactory
	discovered bool
}

// FontBook is an ordered, case-insensitive collection of selectable fonts.
// Registration order is retained so a caller can provide deterministic
// candidate selection without relying on host-installed fonts.
type FontBook struct {
	mu      sync.RWMutex
	entries map[string]fontEntry
	order   []string
}

func NewFontBook() *FontBook { return &FontBook{entries: make(map[string]fontEntry)} }

// Register adds a size-specific face under name. An existing name is replaced
// in place, preserving deterministic selection order.
func (b *FontBook) Register(name string, face FontFace) error {
	if b == nil || strings.TrimSpace(name) == "" || face == nil {
		return fmt.Errorf("%w: font name and face are required", ErrInvalidFont)
	}
	return b.register(fontEntry{name: strings.TrimSpace(name), face: face})
}

// Add is an alias for Register, useful when constructing a book incrementally.
func (b *FontBook) Add(name string, face FontFace) error { return b.Register(name, face) }

// RegisterFont is a descriptive alias for Register.
func (b *FontBook) RegisterFont(name string, face FontFace) error { return b.Register(name, face) }

// RegisterFactory registers a font source which can produce a face at the
// requested size. The factory owns each returned face and must not return nil.
func (b *FontBook) RegisterFactory(name string, factory FontFactory) error {
	if b == nil || strings.TrimSpace(name) == "" || factory == nil {
		return fmt.Errorf("%w: font name and factory are required", ErrInvalidFont)
	}
	return b.register(fontEntry{name: strings.TrimSpace(name), factory: factory})
}

func (b *FontBook) register(entry fontEntry) error {
	key := fontKey(entry.name)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = make(map[string]fontEntry)
	}
	if _, ok := b.entries[key]; !ok {
		b.order = append(b.order, key)
	}
	b.entries[key] = entry
	return nil
}

// Lookup returns a registered size-specific face. Factories are not invoked by
// Lookup; use Select for size-aware selection.
func (b *FontBook) Lookup(name string) (FontFace, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.RLock()
	entry, ok := b.entries[fontKey(name)]
	b.mu.RUnlock()
	if !ok || entry.face == nil {
		return nil, false
	}
	return entry.face, true
}

// Select tries comma-separated candidates in order, matching names
// case-insensitively. An empty candidate list is invalid.
func (b *FontBook) Select(candidates string, size float64) (FontFace, error) {
	trimmed := strings.TrimSpace(candidates)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty font name", ErrInvalidFont)
	}
	if b == nil {
		return nil, fmt.Errorf("%w: no font book", ErrUnsupportedFont)
	}
	for _, candidate := range strings.Split(trimmed, ",") {
		name := strings.TrimSpace(candidate)
		if name == "" {
			continue
		}
		b.mu.RLock()
		entry, ok := b.entries[fontKey(name)]
		b.mu.RUnlock()
		if !ok {
			continue
		}
		if entry.face != nil {
			return entry.face, nil
		}
		face, err := entry.factory(size)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrInvalidFont, entry.name, err)
		}
		if face == nil {
			return nil, fmt.Errorf("%w: %s returned no face", ErrInvalidFont, entry.name)
		}
		return face, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedFont, trimmed)
}

// Names returns the registration names in deterministic order.
func (b *FontBook) Names() []string {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.order))
	for _, key := range b.order {
		if entry, ok := b.entries[key]; ok {
			names = append(names, entry.name)
		}
	}
	return names
}

func fontKey(name string) string {
	name = norm.NFKC.String(strings.TrimSpace(name))
	var key strings.Builder
	space := false
	for _, r := range name {
		if unicode.IsSpace(r) {
			space = key.Len() > 0
			continue
		}
		if space {
			key.WriteByte(' ')
			space = false
		}
		key.WriteRune(unicode.ToLower(r))
	}
	return key.String()
}

// ParseOpenType parses a TrueType/OpenType file into a factory suitable for a
// FontBook. The returned factory creates real glyph-producing faces; no fallback
// rasterizer is involved.
func ParseOpenType(data []byte) (FontFactory, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty font data", ErrInvalidFont)
	}
	parsed, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidFont, err)
	}
	return func(size float64) (FontFace, error) {
		if size <= 0 {
			return nil, fmt.Errorf("font size must be positive")
		}
		return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}, nil
}

// FontSystem binds selection and metrics to a FontBook. It has no platform or
// window dependency and is therefore suitable for headless layout.
type FontSystem struct {
	Book        *FontBook
	DefaultFont string
	DefaultSize float64
}

const defaultFontFaceName = "ＭＳ ゴシック"

func NewFontSystem(book *FontBook) *FontSystem {
	return &FontSystem{Book: book, DefaultFont: defaultFontFaceName, DefaultSize: 16}
}

func (s *FontSystem) Select(name string, size float64) (FontFace, error) {
	if s == nil || s.Book == nil {
		return nil, fmt.Errorf("%w: no font book", ErrUnsupportedFont)
	}
	if size <= 0 {
		size = s.DefaultSize
	}
	if size <= 0 {
		return nil, fmt.Errorf("%w: font size must be positive", ErrInvalidFont)
	}

	requested := strings.TrimSpace(name)
	if strings.HasPrefix(requested, "@") {
		// C++ treats a leading @ as vertical-font state, then resolves the
		// underlying face name. Shaping remains the caller's responsibility.
		requested = strings.TrimSpace(strings.TrimPrefix(requested, "@"))
	}
	var selectionErrors []error
	if requested != "" {
		face, err := s.Book.Select(requested, size)
		if err == nil {
			return face, nil
		}
		if !errors.Is(err, ErrUnsupportedFont) {
			return nil, err
		}
		selectionErrors = append(selectionErrors, err)
	}

	defaultName := strings.TrimSpace(s.DefaultFont)
	if defaultName != "" && fontKey(requested) != fontKey(defaultName) {
		face, err := s.Book.Select(defaultName, size)
		if err == nil {
			return face, nil
		}
		if !errors.Is(err, ErrUnsupportedFont) {
			return nil, err
		}
		selectionErrors = append(selectionErrors, err)
	}

	face, err := s.Book.selectFirst(size, false)
	if err == nil {
		return face, nil
	}
	if !errors.Is(err, ErrUnsupportedFont) {
		return nil, err
	}
	selectionErrors = append(selectionErrors, err)

	candidates := systemFontFallbackCandidates(requested)
	for _, candidate := range candidates {
		face, err := s.Book.Select(candidate, size)
		if err == nil {
			return face, nil
		}
		if !errors.Is(err, ErrUnsupportedFont) {
			return nil, err
		}
	}
	if len(candidates) > 0 {
		selectionErrors = append(selectionErrors, fmt.Errorf("%w: no registered platform fallback family from [%s]", ErrUnsupportedFont, strings.Join(candidates, ", ")))
	}

	face, err = s.Book.selectFirst(size, true)
	if err == nil {
		return face, nil
	}
	if !errors.Is(err, ErrUnsupportedFont) {
		return nil, err
	}
	selectionErrors = append(selectionErrors, err)
	return nil, errors.Join(selectionErrors...)
}

func (b *FontBook) selectFirst(size float64, discovered bool) (FontFace, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: no font book", ErrUnsupportedFont)
	}
	b.mu.RLock()
	var entry fontEntry
	for _, key := range b.order {
		if candidate, ok := b.entries[key]; ok && candidate.discovered == discovered {
			entry = candidate
			break
		}
	}
	b.mu.RUnlock()
	if entry.name == "" {
		if discovered {
			return nil, fmt.Errorf("%w: no discovered system fonts", ErrUnsupportedFont)
		}
		return nil, fmt.Errorf("%w: no explicitly registered fonts", ErrUnsupportedFont)
	}
	if entry.face != nil {
		return entry.face, nil
	}
	face, err := entry.factory(size)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidFont, entry.name, err)
	}
	if face == nil {
		return nil, fmt.Errorf("%w: %s returned no face", ErrInvalidFont, entry.name)
	}
	return face, nil
}

// FaceMetrics exposes deterministic integer pixel metrics from a selected face.
type FaceMetrics struct {
	Ascent    int
	Descent   int
	Height    int
	XHeight   int
	CapHeight int
}

func MetricsForFace(face FontFace) (FaceMetrics, error) {
	if face == nil {
		return FaceMetrics{}, fmt.Errorf("%w: nil face", ErrInvalidFont)
	}
	m := face.Metrics()
	ascent, descent, height := m.Ascent.Ceil(), m.Descent.Ceil(), m.Height.Ceil()
	if height < ascent+descent {
		height = ascent + descent
	}
	if height < 1 {
		height = 1
	}
	return FaceMetrics{Ascent: ascent, Descent: descent, Height: height, XHeight: m.XHeight.Ceil(), CapHeight: m.CapHeight.Ceil()}, nil
}
