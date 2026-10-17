package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// FontState is the script-visible, deterministic font selection state. The
// selected face is resolved only through Services.FontSystem; no host fallback
// is attempted.
type FontState struct {
	Face      string
	Height    int32
	Bold      bool
	Italic    bool
	Underline bool
	Strikeout bool
	Angle     int32
}

func newFontState() *FontState {
	return &FontState{Face: defaultFontFaceName, Height: -12}
}

func (s *Services) SetFontBook(book *FontBook) {
	s.ensureInit()
	if book == nil {
		book = NewFontBook()
	}
	s.mu.Lock()
	s.FontBook = book
	if s.FontSystem == nil {
		s.FontSystem = NewFontSystem(book)
	} else {
		s.FontSystem.Book = book
	}
	s.mu.Unlock()
}

func (s *Services) SetFontSystem(system *FontSystem) {
	s.ensureInit()
	if system == nil {
		s.mu.Lock()
		s.FontSystem = NewFontSystem(s.FontBook)
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if system.Book == nil {
		system.Book = s.FontBook
	}
	if strings.TrimSpace(system.DefaultFont) == "" {
		system.DefaultFont = defaultFontFaceName
	}
	s.FontSystem = system
	s.FontBook = system.Book
	s.mu.Unlock()
}

func (s *Services) RegisterFontFace(name string, face FontFace) error {
	s.ensureInit()
	s.mu.RLock()
	book := s.FontBook
	s.mu.RUnlock()
	return book.Register(name, face)
}

func (s *Services) RegisterFont(name string, face FontFace) error {
	return s.RegisterFontFace(name, face)
}

func (s *Services) RegisterFontFactory(name string, factory FontFactory) error {
	s.ensureInit()
	s.mu.RLock()
	book := s.FontBook
	s.mu.RUnlock()
	return book.RegisterFactory(name, factory)
}

func (s *Services) Font(id tjs.ObjectID) *FontState {
	if s == nil {
		return nil
	}
	s.ensureInit()
	s.mu.RLock()
	f := s.Fonts[id]
	s.mu.RUnlock()
	return f
}

func (s *Services) fontFace(f *FontState) (FontFace, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: nil Font state", ErrInvalidFont)
	}
	s.ensureInit()
	state := *f
	s.mu.RLock()
	if s.FontSystem == nil {
		s.mu.RUnlock()
		return nil, fmt.Errorf("%w: no font system", ErrUnsupportedFont)
	}
	system := *s.FontSystem
	s.mu.RUnlock()
	size := math.Abs(float64(state.Height))
	if size == 0 {
		size = system.DefaultSize
	}
	face, err := system.Select(state.Face, size)
	if err != nil {
		return nil, err
	}
	if system.Book != nil {
		face = withGlyphFallback(withGlyphFallbackChain(face, system.Book, state.Face, size))
	} else {
		face = withGlyphFallback(face)
	}
	key := prerenderedFontMapKeyFor(state)
	s.mu.RLock()
	storage := s.prerenderedFontMappings[key]
	s.mu.RUnlock()
	if !state.Bold && !state.Italic {
		if storage != "" {
			prerendered, err := s.loadPrerenderedFont(storage)
			if err != nil {
				return nil, err
			}
			return &prerenderedFontFace{FontFace: face, font: prerendered}, nil
		}
		return face, nil
	}
	face = &styledFontFace{FontFace: face, bold: state.Bold, italic: state.Italic}
	if storage != "" {
		prerendered, err := s.loadPrerenderedFont(storage)
		if err != nil {
			return nil, err
		}
		face = &prerenderedFontFace{FontFace: face, font: prerendered}
	}
	return face, nil
}

func (s *Services) fontNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Font requires non-null object context")
	}
	id := context.ObjectRef().Object
	s.ensureInit()
	if operation == "@initialize" || operation == "Font" {
		f := newFontState()
		if len(args) > 0 && args[0].Kind() != tjs.KindVoid {
			f.Face = args[0].Text()
		}
		if len(args) > 1 && args[1].Kind() != tjs.KindVoid {
			n, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("Font.height: %w", err)
			}
			if n < math.MinInt32 || n > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Font.height exceeds int32")
			}
			f.Height = int32(n)
		}
		s.mu.Lock()
		if s.Fonts[id] != nil {
			s.mu.Unlock()
			return tjs.VoidValue, nil
		}
		if len(s.Fonts) >= 10_000 {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Font count limit exceeded")
		}
		s.Fonts[id] = f
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.Fonts, id)
		for layerID, fontID := range s.LayerFonts {
			if fontID == id {
				delete(s.LayerFonts, layerID)
				delete(s.LayerDefaultFonts, layerID)
			}
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	fontState := s.Fonts[id]
	if fontState != nil {
		stateCopy := *fontState
		fontState = &stateCopy
	}
	s.mu.RUnlock()
	f := fontState
	if f == nil {
		return tjs.VoidValue, fmt.Errorf("Font.%s: context has no Font native instance", operation)
	}
	arg := func(i int) (tjs.Value, error) {
		if i < 0 || i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("Font.%s: missing argument %d", operation, i)
		}
		return args[i], nil
	}
	if strings.HasPrefix(operation, "get:") {
		switch strings.TrimPrefix(operation, "get:") {
		case "face":
			return tjs.StringValue(f.Face), nil
		case "height":
			return tjs.IntegerValue(int64(f.Height)), nil
		case "bold":
			return tjs.IntegerValue(boolInt(f.Bold)), nil
		case "italic":
			return tjs.IntegerValue(boolInt(f.Italic)), nil
		case "underline":
			return tjs.IntegerValue(boolInt(f.Underline)), nil
		case "strikeout":
			return tjs.IntegerValue(boolInt(f.Strikeout)), nil
		case "angle":
			return tjs.IntegerValue(int64(f.Angle)), nil
		default:
			return tjs.VoidValue, fmt.Errorf("%w: unsupported Font property %s", ErrUnsupportedNative, operation)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		f = s.Fonts[id]
		if f == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Font.%s: context has no Font native instance", operation)
		}
		defer s.mu.Unlock()
		switch strings.TrimPrefix(operation, "set:") {
		case "face":
			f.Face = value.Text()
		case "height":
			n, e := value.Int()
			if e != nil {
				return tjs.VoidValue, e
			}
			if n < math.MinInt32 || n > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Font.height exceeds int32")
			}
			f.Height = int32(n)
		case "bold":
			b, e := value.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			f.Bold = b
		case "italic":
			b, e := value.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			f.Italic = b
		case "underline":
			b, e := value.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			f.Underline = b
		case "strikeout":
			b, e := value.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			f.Strikeout = b
		case "angle":
			n, e := value.Int()
			if e != nil {
				return tjs.VoidValue, e
			}
			if n < math.MinInt32 || n > math.MaxInt32 {
				return tjs.VoidValue, fmt.Errorf("Font.angle exceeds int32")
			}
			f.Angle = int32(n)
		default:
			return tjs.VoidValue, fmt.Errorf("%w: unsupported Font property %s", ErrUnsupportedNative, operation)
		}
		return tjs.VoidValue, err
	}
	switch operation {
	case "mapPrerenderedFont":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		storage := value.Text()
		if storage == "" {
			return tjs.VoidValue, fmt.Errorf("Font.mapPrerenderedFont requires a storage name")
		}
		if _, err := s.loadPrerenderedFont(storage); err != nil {
			return tjs.VoidValue, fmt.Errorf("Font.mapPrerenderedFont %q: %w", storage, err)
		}
		s.mu.Lock()
		if current := s.Fonts[id]; current == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Font.mapPrerenderedFont: context has no Font native instance")
		} else {
			s.prerenderedFontMappings[prerenderedFontMapKeyFor(*current)] = storage
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "unmapPrerenderedFont":
		s.mu.Lock()
		current := s.Fonts[id]
		if current == nil {
			s.mu.Unlock()
			return tjs.VoidValue, fmt.Errorf("Font.unmapPrerenderedFont: context has no Font native instance")
		}
		delete(s.prerenderedFontMappings, prerenderedFontMapKeyFor(*current))
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "getList":
		s.mu.RLock()
		book := s.FontBook
		s.mu.RUnlock()
		names := book.Names()
		items := make([]tjs.Value, len(names))
		for i, name := range names {
			items[i] = tjs.StringValue(name)
		}
		return vm.NewArray(items)
	case "getTextWidth", "getTextHeight", "getEscWidthX", "getEscWidthY", "getEscHeightX", "getEscHeightY":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		text := value.Text()
		if err := chargeTextBudget(budget, len(text)); err != nil {
			return tjs.VoidValue, err
		}
		height := math.Abs(float64(f.Height))
		switch operation {
		case "getTextHeight":
			return tjs.IntegerValue(int64(height)), nil
		case "getEscHeightX":
			return tjs.RealValue(math.Sin(float64(f.Angle)*math.Pi/1800) * height), nil
		case "getEscHeightY":
			return tjs.RealValue(math.Cos(float64(f.Angle)*math.Pi/1800) * height), nil
		}
		face, err := s.fontFace(f)
		if err != nil {
			return tjs.VoidValue, err
		}
		width, err := fontTextWidth(face, text)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "getTextWidth" {
			return tjs.IntegerValue(int64(width)), nil
		}
		angle := float64(f.Angle) * math.Pi / 1800
		if operation == "getEscWidthX" {
			return tjs.RealValue(math.Cos(angle) * float64(width)), nil
		}
		return tjs.RealValue(math.Sin(angle) * -float64(width)), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Font.%s", ErrUnsupportedNative, operation)
	}
}

func chargeTextBudget(budget *uint64, n int) error {
	if budget == nil {
		return nil
	}
	cost := uint64(n) + 1
	if cost > *budget {
		return ErrBudgetExceeded
	}
	*budget -= cost
	return nil
}

// fontTextWidth mirrors C++ Font's direct width measurement: glyph advances
// are rounded and accumulated independently, without shaping or kerning.
func fontTextWidth(face FontFace, text string) (int, error) {
	width := 0
	for _, r := range text {
		if r == '\r' || r == '\n' {
			continue
		}
		advance, ok := face.GlyphAdvance(r)
		if !ok {
			return 0, fmt.Errorf("%w: glyph U+%04X in selected face", ErrUnsupportedFont, r)
		}
		width += fixedToPixels(advance)
	}
	return width, nil
}
