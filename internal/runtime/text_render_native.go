package runtime

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/image/math/fixed"
)

const (
	textRenderMaxUnits      = 4 << 20
	textRenderMaxCharacters = 100_000
	textRenderMaxObjects    = 10_000
)

type textRenderCharacter struct {
	text   []uint16
	format map[string]tjs.Value
	size   float32
	width  float32
	delay  float32
	x, y   float32
}
type textRenderLine struct {
	characters              []textRenderCharacter
	height, spacing, offset float32
}
type textRenderState struct {
	mu                                  sync.Mutex
	defaults, current                   map[string]tjs.Value
	options                             map[string]tjs.Value
	width, height, fontScale, timeScale float32
	vertical, initialized, busy, over   bool
	lines                               []textRenderLine
	pending, published                  []textRenderCharacter
	renderText                          []uint16
	count                               int
	delay                               float32
	left, right, top, bottom            float32
	keyWaits                            []int
	lineIndent, nextIndent              float32
}

func textRenderDefaults() map[string]tjs.Value {
	return map[string]tjs.Value{
		"Align": tjs.IntegerValue(-1), "BigFontSize": tjs.RealValue(48), "Bold": tjs.IntegerValue(0),
		"ChColor": tjs.IntegerValue(int64(uint32(0xffffffff))), "Edge": tjs.IntegerValue(0),
		"EdgeColor": tjs.IntegerValue(int64(uint32(4278223103))), "Face": tjs.StringValue("normal"),
		"FontSize": tjs.RealValue(24), "Italic": tjs.IntegerValue(0), "LineSize": tjs.RealValue(24),
		"LineSpacing": tjs.RealValue(6), "Pitch": tjs.RealValue(0), "RubyOffset": tjs.RealValue(-2),
		"RubySize": tjs.RealValue(10), "Shadow": tjs.IntegerValue(1),
		"ShadowColor": tjs.IntegerValue(int64(uint32(0xff000000))), "ShadowDiff": tjs.IntegerValue(1),
		"SmallFontSize": tjs.RealValue(12), "Valign": tjs.IntegerValue(-1),
	}
}
func newTextRenderState() *textRenderState {
	d := textRenderDefaults()
	c := make(map[string]tjs.Value, len(d))
	for k, v := range d {
		c[k] = v
	}
	return &textRenderState{defaults: d, current: c, options: make(map[string]tjs.Value), fontScale: 1, timeScale: 1}
}
func textRenderStyleField(name string) bool {
	switch name {
	case "Align", "Valign", "Pitch", "LineSize", "LineSpacing":
		return true
	}
	return false
}
func textRenderNumeric(v tjs.Value) (float32, error) {
	n, err := v.Float()
	if err != nil {
		return 0, err
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > 1e9 {
		return 0, fmt.Errorf("TextRender numeric range limit exceeded")
	}
	return float32(n), nil
}
func textRenderConvert(v tjs.Value, kind byte) (tjs.Value, error) {
	switch kind {
	case 's':
		return tjs.StringValue(v.Text()), nil
	case 'f':
		n, e := textRenderNumeric(v)
		return tjs.RealValue(float64(n)), e
	case 'b':
		b, e := v.Truth()
		if e != nil {
			return tjs.VoidValue, e
		}
		if b {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	case 'c':
		n, e := v.Int()
		return tjs.IntegerValue(int64(uint32(n))), e
	default:
		n, e := v.Int()
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.IntegerValue(int64(int32(n))), nil
	}
}
func textRenderField(name string) (string, byte, bool) {
	for _, f := range []struct {
		k, a string
		c    byte
	}{{"Align", "align", 'i'}, {"BigFontSize", "bigfontsize", 'f'}, {"Bold", "bold", 'b'}, {"ChColor", "color", 'c'}, {"Edge", "edge", 'b'}, {"EdgeColor", "edgecolor", 'c'}, {"Face", "face", 's'}, {"FontSize", "fontsize", 'f'}, {"Italic", "", 'b'}, {"LineSize", "linesize", 'f'}, {"LineSpacing", "linespacing", 'f'}, {"Pitch", "pitch", 'f'}, {"RubyOffset", "rubyoffset", 'f'}, {"RubySize", "rubysize", 'f'}, {"Shadow", "shadow", 'b'}, {"ShadowColor", "shadowcolor", 'c'}, {"ShadowDiff", "shadowdiff", 'i'}, {"SmallFontSize", "smallfontsize", 'f'}, {"Valign", "valign", 'i'}} {
		if f.k == name {
			return f.a, f.c, true
		}
	}
	return "", 0, false
}
func textRenderNum(m map[string]tjs.Value, key string) float32 {
	n, _ := m[key].Float()
	return float32(n)
}
func textRenderCharge(budget *uint64) error {
	if budget == nil {
		return nil
	}
	if *budget == 0 {
		return ErrBudgetExceeded
	}
	*budget = *budget - 1
	return nil
}
func textRenderArg(args []tjs.Value, i int, op string) (tjs.Value, error) {
	if i < 0 || i >= len(args) {
		return tjs.VoidValue, fmt.Errorf("TextRenderBase.%s: missing argument %d", op, i)
	}
	return args[i], nil
}
func textRenderContext(context tjs.Value) (tjs.ObjectID, error) {
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return 0, fmt.Errorf("TextRenderBase requires a non-null object context")
	}
	return context.ObjectRef().Object, nil
}
func textRenderDictionary(vm *tjs.VM, values map[string]tjs.Value) (tjs.Value, error) {
	out, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	o, ok := vm.Objects.Lookup(out.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("TextRender dictionary allocation failed")
	}
	for k, v := range values {
		if err := o.Set(k, v); err != nil {
			return tjs.VoidValue, err
		}
	}
	return out, nil
}
func textRenderCharacterValue(vm *tjs.VM, c textRenderCharacter) (tjs.Value, error) {
	m := map[string]tjs.Value{
		"face": c.format["Face"], "y": tjs.RealValue(float64(c.y)), "edgeColor": c.format["EdgeColor"],
		"color": c.format["ChColor"], "vertical": tjs.IntegerValue(0), "shadow": c.format["Shadow"],
		"cw": tjs.RealValue(float64(c.width)), "bold": c.format["Bold"], "text": tjs.StringUnits(c.text),
		"delay": tjs.RealValue(float64(c.delay)), "shadowColor": c.format["ShadowColor"], "x": tjs.RealValue(float64(c.x)),
		"edge": c.format["Edge"], "italic": c.format["Italic"], "graph": tjs.IntegerValue(0),
		"shadowDiff": c.format["ShadowDiff"], "size": tjs.RealValue(float64(c.size)),
	}
	return textRenderDictionary(vm, m)
}
func textRenderFace(s *Services, r *textRenderState) (FontFace, error) {
	faceName := r.current["Face"].Text()
	size := float64(textRenderNum(r.current, "FontSize") * r.fontScale)
	if size <= 0 {
		return nil, fmt.Errorf("TextRender font size must be positive")
	}
	s.mu.Lock()
	system := s.FontSystem
	if system != nil {
		if strings.TrimSpace(system.FallbackFont) == "" && system.Book != nil {
			system.FallbackFont = system.Book.availableName(systemCJKFallbackCandidates())
		}
		copy := *system
		system = &copy
	}
	s.mu.Unlock()
	if system == nil {
		return nil, fmt.Errorf("%w: no font system", ErrUnsupportedNative)
	}
	face, err := system.Select(faceName, size)
	if err != nil {
		return nil, err
	}
	fallback, err := system.fixedFallbackFace(size)
	if err != nil {
		return nil, err
	}
	bold := textRenderNum(r.current, "Bold") != 0
	italic := textRenderNum(r.current, "Italic") != 0
	face = withFixedGlyphFallback(face, fallback)
	face = withGlyphFallback(face)
	if bold || italic {
		face = &styledFontFace{FontFace: face, bold: bold, italic: italic}
	}
	return face, nil
}
func textRenderWidth(s *Services, r *textRenderState, units []uint16) (float32, error) {
	face, err := textRenderFace(s, r)
	if err != nil {
		return 0, err
	}
	if len(units) == 0 {
		return 0, nil
	}
	advance, ok := face.GlyphAdvance(utf16.Decode(units)[0])
	if !ok {
		return 0, fmt.Errorf("%w: glyph U+%04X", ErrUnsupportedNative, units[0])
	}
	return float32((advance + fixed.Int26_6(63)) / 64), nil
}
func textRenderOffset(r *textRenderState) float32 {
	if len(r.lines) == 0 {
		return 0
	}
	last := r.lines[len(r.lines)-1]
	return last.offset + last.height + last.spacing
}
func (s *Services) textRenderFinishLine(r *textRenderState, budget *uint64) error {
	if len(r.pending) == 0 || r.over {
		return nil
	}
	h := textRenderNum(r.current, "LineSize") * r.fontScale
	for _, c := range r.pending {
		if c.size > h {
			h = c.size
		}
	}
	y := textRenderOffset(r)
	limit := r.height
	if r.vertical {
		limit = r.width
	}
	if limit > 0 && y+h > limit {
		r.over = true
		r.pending = nil
		return nil
	}
	size := r.pendingWidth()
	limit = r.width
	if r.vertical {
		limit = r.height
	}
	align := textRenderNum(r.current, "Align")
	x := r.lineIndent
	if align == 0 {
		x = float32(math.Trunc(float64((limit - size) / 2)))
	} else if align == 1 {
		x = limit - size
	}
	line := make([]textRenderCharacter, len(r.pending))
	for i := range r.pending {
		if err := textRenderCharge(budget); err != nil {
			return err
		}
		c := r.pending[i]
		if r.vertical {
			c.x = r.width - y
			c.y = x
		} else {
			c.x = x
			c.y = y + h - c.size
		}
		line[i] = c
		r.renderText = append(r.renderText, c.text...)
		x += c.width + textRenderNum(c.format, "Pitch")
	}
	r.renderText = append(r.renderText, '\n')
	r.lines = append(r.lines, textRenderLine{characters: line, height: h, spacing: textRenderNum(r.current, "LineSpacing"), offset: y})
	r.pending = nil
	r.lineIndent = r.nextIndent
	r.published = nil
	return nil
}
func (r *textRenderState) pendingWidth() float32 {
	var n float32
	for _, c := range r.pending {
		n += c.width + textRenderNum(c.format, "Pitch")
	}
	return n
}
func (s *Services) textRenderDone(r *textRenderState) {
	r.published = r.published[:0]
	for _, l := range r.lines {
		r.published = append(r.published, l.characters...)
	}
	r.left, r.right, r.top, r.bottom = 0, 0, 0, 0
	if r.vertical {
		r.left = r.width
	}
	for _, c := range r.published {
		if r.vertical {
			if c.x-c.size < r.left {
				r.left = c.x - c.size
			}
			if c.y+c.size > r.bottom {
				r.bottom = c.y + c.size
			}
		} else {
			if c.x < r.left {
				r.left = c.x
			}
			if c.x+c.width > r.right {
				r.right = c.x + c.width
			}
			if c.y < r.top {
				r.top = c.y
			}
			if c.y+c.size > r.bottom {
				r.bottom = c.y + c.size
			}
		}
	}
	if len(r.published) != 0 {
		available := r.height - r.bottom
		if r.vertical {
			available = -(r.width - r.left)
		}
		valign := textRenderNum(r.current, "Valign")
		var off float32
		if valign == 0 {
			off = float32(math.Trunc(float64(available / 2)))
		} else if valign == 1 {
			off = available
		}
		for i := range r.published {
			if r.vertical {
				r.published[i].x += off
			} else {
				r.published[i].y += off
			}
		}
		if r.vertical {
			r.left += off
			r.right += off
		} else {
			r.top += off
			r.bottom += off
		}
	}
}
func (s *Services) textRenderRender(r *textRenderState, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if len(args) < 5 {
		return tjs.VoidValue, fmt.Errorf("TextRenderBase.render requires five arguments")
	}
	units := args[0].UTF16()
	if len(units) > textRenderMaxUnits {
		return tjs.VoidValue, fmt.Errorf("TextRender input limit exceeded")
	}
	auto, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	base, err := textRenderNumeric(args[2])
	if err != nil {
		return tjs.VoidValue, err
	}
	total, err := textRenderNumeric(args[3])
	if err != nil {
		return tjs.VoidValue, err
	}
	same, err := args[4].Truth()
	if err != nil {
		return tjs.VoidValue, err
	}
	if !same {
		for li := range r.lines {
			for ci := range r.lines[li].characters {
				r.lines[li].characters[ci].delay = 0
			}
		}
		for ci := range r.pending {
			r.pending[ci].delay = 0
		}
	}
	for _, name := range []string{"word_break", "width_time_scale"} {
		if value, ok := r.options[name]; ok {
			enabled, e := value.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			if enabled {
				return tjs.VoidValue, fmt.Errorf("%w: TextRender layout option %s", ErrUnsupportedNative, name)
			}
		}
	}
	start := r.count
	delay, step := float32(0), base
	for i := 0; i < len(units) && units[i] != 0 && !r.over; {
		if err := textRenderCharge(budget); err != nil {
			return tjs.VoidValue, err
		}
		u := units[i]
		i++
		if u == '\\' {
			if i >= len(units) {
				return tjs.VoidValue, fmt.Errorf("unterminated TextRender escape")
			}
			esc := units[i]
			i++
			switch esc {
			case 'n':
				if err := s.textRenderFinishLine(r, budget); err != nil {
					return tjs.VoidValue, err
				}
				continue
			case 'k':
				r.keyWaits = append(r.keyWaits, r.count)
				continue
			case 't':
				u = '\t'
			case 'x':
				continue
			default:
				u = esc
			}
		}
		if u == '%' {
			if i >= len(units) {
				return tjs.VoidValue, fmt.Errorf("unterminated TextRender format command")
			}
			cmd := units[i]
			i++
			switch cmd {
			case 'b', 'i', 's', 'e':
				if i >= len(units) {
					return tjs.VoidValue, fmt.Errorf("missing TextRender format flag")
				}
				flag := units[i]
				i++
				key := map[uint16]string{'b': "Bold", 'i': "Italic", 's': "Shadow", 'e': "Edge"}[cmd]
				if flag == '0' {
					r.current[key] = tjs.IntegerValue(0)
				} else if flag == '1' {
					r.current[key] = tjs.IntegerValue(1)
				} else {
					r.current[key] = r.defaults[key]
				}
			case 'B':
				r.current["FontSize"] = r.defaults["BigFontSize"]
			case 'S':
				r.current["FontSize"] = r.defaults["SmallFontSize"]
			case 'r':
				r.current = cloneTextRenderValues(r.defaults)
			case 'C':
				r.current["Align"] = tjs.IntegerValue(0)
			case 'R':
				r.current["Align"] = tjs.IntegerValue(1)
			case 'L':
				r.current["Align"] = tjs.IntegerValue(-1)
			case 'f':
				token, n, e := textRenderToken(units, i, ';')
				if e != nil {
					return tjs.VoidValue, e
				}
				i = n
				r.current["Face"] = tjs.StringUnits(token)
			case 'p', 'd', 'w':
				token, n, e := textRenderToken(units, i, ';')
				if e != nil {
					return tjs.VoidValue, e
				}
				i = n
				v, e := strconv.ParseFloat(tjs.UTF16ToString(token), 32)
				if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return tjs.VoidValue, fmt.Errorf("invalid TextRender numeric command")
				}
				if cmd == 'p' {
					r.current["Pitch"] = tjs.RealValue(v)
				} else if cmd == 'd' {
					step = base * float32(v) / 100
				} else {
					delay += base * float32(v) / 100
				}
			case 'D':
				return tjs.VoidValue, fmt.Errorf("%w: TextRender label callback", ErrUnsupportedNative)
			default:
				return tjs.VoidValue, fmt.Errorf("%w: TextRender format command %q", ErrUnsupportedNative, string(rune(cmd)))
			}
			continue
		}
		if u == '#' {
			token, n, e := textRenderToken(units, i, ';')
			if e != nil {
				return tjs.VoidValue, e
			}
			i = n
			v, e := strconv.ParseUint(tjs.UTF16ToString(token), 16, 32)
			if e != nil {
				return tjs.VoidValue, fmt.Errorf("invalid TextRender color")
			}
			r.current["ChColor"] = tjs.IntegerValue(int64(uint32(v) | 0xff000000))
			continue
		}
		if u == '$' {
			return tjs.VoidValue, fmt.Errorf("%w: TextRender eval callback", ErrUnsupportedNative)
		}
		if u == '[' || u == '&' {
			return tjs.VoidValue, fmt.Errorf("%w: TextRender graphic/ruby layout", ErrUnsupportedNative)
		}
		if u == '\r' {
			continue
		}
		if u == '\n' {
			if err := s.textRenderFinishLine(r, budget); err != nil {
				return tjs.VoidValue, err
			}
			continue
		}
		text := []uint16{u}
		if u >= 0xd800 && u <= 0xdbff && i < len(units) && units[i] >= 0xdc00 && units[i] <= 0xdfff {
			text = append(text, units[i])
			i++
		}
		width, e := textRenderWidth(s, r, text)
		if e != nil {
			return tjs.VoidValue, e
		}
		size := textRenderNum(r.current, "FontSize") * r.fontScale
		limit := r.width
		if r.vertical {
			limit = r.height
		}
		if limit > 0 && r.lineIndent+r.pendingWidth()+width+textRenderNum(r.current, "Pitch") > limit && len(r.pending) > 0 {
			if e := s.textRenderFinishLine(r, budget); e != nil {
				return tjs.VoidValue, e
			}
			if r.over {
				break
			}
			delay += step
		}
		if auto < 0 && r.count == start && len(r.pending) == 0 {
			_ = auto
		}
		c := textRenderCharacter{text: text, format: cloneTextRenderValues(r.current), size: size, width: width, delay: delay}
		r.pending = append(r.pending, c)
		r.count++
		r.delay = delay + base
		delay += step
		if r.count > textRenderMaxCharacters {
			return tjs.VoidValue, fmt.Errorf("TextRender character limit exceeded")
		}
	}
	if total > 0 {
		if r.delay != 0 {
			factor := total / r.delay
			for i := start; i < len(r.pending); i++ {
				r.pending[i].delay *= factor
			}
			for li := range r.lines {
				for ci := range r.lines[li].characters {
					if li+ci >= start {
						r.lines[li].characters[ci].delay *= factor
					}
				}
			}
		}
		r.delay = total
	}
	return tjs.IntegerValue(boolInt(!r.over)), nil
}
func cloneTextRenderValues(in map[string]tjs.Value) map[string]tjs.Value {
	out := make(map[string]tjs.Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func textRenderToken(units []uint16, i int, end uint16) ([]uint16, int, error) {
	start := i
	for i < len(units) && units[i] != end {
		i++
	}
	if i >= len(units) {
		return nil, i, fmt.Errorf("unterminated TextRender command")
	}
	return append([]uint16(nil), units[start:i]...), i + 1, nil
}
func (s *Services) textRenderNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	id, err := textRenderContext(context)
	if err != nil {
		return tjs.VoidValue, err
	}
	s.ensureInit()
	if operation == "@initialize" || operation == "TextRenderBase" {
		s.mu.Lock()
		if s.TextRenderers[id] == nil {
			if len(s.TextRenderers) >= textRenderMaxObjects {
				s.mu.Unlock()
				return tjs.VoidValue, fmt.Errorf("TextRender count limit exceeded")
			}
			s.TextRenderers[id] = newTextRenderState()
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.TextRenderers, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	r := s.TextRenderers[id]
	s.mu.RUnlock()
	if r == nil {
		return tjs.VoidValue, fmt.Errorf("TextRenderBase.%s: context has no native instance", operation)
	}
	if operation == "onEval" {
		if context.Kind() != tjs.KindObject || context.IsNull() {
			return tjs.VoidValue, fmt.Errorf("TextRenderBase.onEval requires object context")
		}
		callback, e := tjs.NewDispatcher(vm.Objects).Get(context, "onEval")
		if e != nil {
			return tjs.VoidValue, e
		}
		if callback.IsVoid() {
			return tjs.VoidValue, fmt.Errorf("%w: TextRender onEval callback is absent", ErrUnsupportedNative)
		}
		value, e := vm.CallFunction(callback, context, args, s, budget)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.StringValue(value.Text()), nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.HasPrefix(operation, "get:") {
		name := strings.TrimPrefix(operation, "get:")
		if strings.HasPrefix(name, "default") {
			field := strings.TrimPrefix(name, "default")
			value, ok := r.defaults[field]
			if !ok {
				return tjs.VoidValue, fmt.Errorf("%w: TextRenderBase property %s", ErrUnsupportedNative, name)
			}
			return value, nil
		}
	}
	if strings.HasPrefix(operation, "get:") {
		name := strings.TrimPrefix(operation, "get:")
		if v, ok := r.defaults[strings.TrimPrefix(name, "default")]; strings.HasPrefix(name, "default") && ok {
			return v, nil
		}
		switch name {
		case "fontScale":
			return tjs.RealValue(float64(r.fontScale)), nil
		case "timeScale":
			return tjs.RealValue(float64(r.timeScale)), nil
		case "vertical":
			return tjs.IntegerValue(boolInt(r.vertical)), nil
		}
		if !r.initialized {
			return tjs.VoidValue, fmt.Errorf("TextRender layout requires clear or setRenderSize")
		}
		switch name {
		case "renderText":
			return tjs.StringUnits(r.renderText), nil
		case "renderCount":
			return tjs.IntegerValue(int64(r.count)), nil
		case "renderLines":
			return tjs.IntegerValue(int64(len(r.lines))), nil
		case "renderDelay":
			return tjs.RealValue(float64(r.delay * r.timeScale)), nil
		case "renderOver":
			return tjs.IntegerValue(boolInt(r.over)), nil
		case "renderLeft":
			return tjs.RealValue(float64(r.left)), nil
		case "renderRight":
			return tjs.RealValue(float64(r.right)), nil
		case "renderTop":
			return tjs.RealValue(float64(r.top)), nil
		case "renderBottom":
			return tjs.RealValue(float64(r.bottom)), nil
		case "maxScrollOffset":
			if r.vertical {
				return tjs.RealValue(float64(r.width - r.left)), nil
			}
			return tjs.RealValue(float64(r.height - r.bottom)), nil
		case "maxScrollLine":
			return tjs.IntegerValue(0), nil
		default:
			return tjs.VoidValue, fmt.Errorf("%w: TextRenderBase property %s", ErrUnsupportedNative, name)
		}
	}
	if strings.HasPrefix(operation, "set:") {
		name := strings.TrimPrefix(operation, "set:")
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if strings.HasPrefix(name, "default") {
			field := strings.TrimPrefix(name, "default")
			_, kind, ok := textRenderField(field)
			if !ok {
				return tjs.VoidValue, fmt.Errorf("%w: unknown TextRender field %s", ErrUnsupportedNative, field)
			}
			x, e := textRenderConvert(v, kind)
			if e != nil {
				return tjs.VoidValue, e
			}
			r.defaults[field] = x
			return tjs.VoidValue, nil
		}
		switch name {
		case "fontScale", "timeScale":
			x, e := textRenderNumeric(v)
			if e != nil {
				return tjs.VoidValue, e
			}
			if name == "fontScale" {
				r.fontScale = x
			} else {
				r.timeScale = x
			}
		case "vertical":
			b, e := v.Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			r.vertical = b
		default:
			return tjs.VoidValue, fmt.Errorf("%w: TextRenderBase property %s", ErrUnsupportedNative, name)
		}
		return tjs.VoidValue, nil
	}
	if operation == "setDefault" {
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if v.Kind() != tjs.KindObject || v.IsNull() {
			return tjs.VoidValue, fmt.Errorf("TextRender settings require an object")
		}
		o, ok := vm.Objects.Lookup(v.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("TextRender settings object is invalid")
		}
		for field, attribute := range textRenderAttributes() {
			if attribute == "" {
				continue
			}
			x, exists := o.Get(attribute)
			if !exists || x.IsVoid() {
				continue
			}
			cv, e := textRenderConvert(x, textRenderFieldKind(field))
			if e != nil {
				return tjs.VoidValue, e
			}
			r.defaults[field] = cv
		}
		return tjs.VoidValue, nil
	}
	switch operation {
	case "clear", "setRenderSize":
		if operation == "setRenderSize" {
			a, e := textRenderArg(args, 0, operation)
			if e != nil {
				return tjs.VoidValue, e
			}
			b, e := textRenderArg(args, 1, operation)
			if e != nil {
				return tjs.VoidValue, e
			}
			r.width, e = textRenderNumeric(a)
			if e != nil {
				return tjs.VoidValue, e
			}
			r.height, e = textRenderNumeric(b)
			if e != nil {
				return tjs.VoidValue, e
			}
		}
		r.current = cloneTextRenderValues(r.defaults)
		r.lines = nil
		r.pending = nil
		r.published = nil
		r.renderText = nil
		r.count = 0
		r.delay = 0
		r.over = false
		r.keyWaits = nil
		r.lineIndent = 0
		r.nextIndent = 0
		r.left = 0
		r.right = 0
		r.top = 0
		r.bottom = 0
		r.initialized = true
		return tjs.VoidValue, nil
	case "resetFont", "resetStyle":
		for k, v := range r.defaults {
			if textRenderStyleField(k) == (operation == "resetStyle") {
				r.current[k] = v
			}
		}
		return tjs.VoidValue, nil
	case "setDefault", "setFont", "setStyle":
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if v.Kind() != tjs.KindObject || v.IsNull() {
			return tjs.VoidValue, fmt.Errorf("TextRender settings require an object")
		}
		o, ok := vm.Objects.Lookup(v.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("TextRender settings object is invalid")
		}
		for field, attribute := range textRenderAttributes() {
			if attribute == "" {
				continue
			}
			if (operation == "setStyle") != textRenderStyleField(field) {
				continue
			}
			x, exists := o.Get(attribute)
			if !exists || x.IsVoid() {
				continue
			}
			kind := textRenderFieldKind(field)
			cv, e := textRenderConvert(x, kind)
			if e != nil {
				return tjs.VoidValue, e
			}
			if operation == "setDefault" {
				r.defaults[field] = cv
			} else {
				r.current[field] = cv
			}
		}
		return tjs.VoidValue, nil
	case "setOption":
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		if v.Kind() != tjs.KindObject || v.IsNull() {
			return tjs.VoidValue, fmt.Errorf("TextRender options require an object")
		}
		o, ok := vm.Objects.Lookup(v.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("TextRender options object is invalid")
		}
		for _, name := range []string{"following", "leading", "begin", "end", "kinsoku_max", "vertical", "word_break", "width_time_scale"} {
			if x, yes := o.Get(name); yes && !x.IsVoid() {
				if x.Kind() == tjs.KindObject {
					return tjs.VoidValue, fmt.Errorf("TextRender option %s requires a scalar", name)
				}
				if name == "vertical" {
					b, e := x.Truth()
					if e != nil {
						return tjs.VoidValue, e
					}
					r.vertical = b
				} else {
					r.options[name] = x
				}
			}
		}
		return tjs.VoidValue, nil
	case "getCharacters":
		a, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		b, e := textRenderArg(args, 1, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		start, e := a.Int()
		if e != nil {
			return tjs.VoidValue, e
		}
		count, e := b.Int()
		if e != nil {
			return tjs.VoidValue, e
		}
		if start < 0 || count < 0 {
			return tjs.VoidValue, fmt.Errorf("TextRender character range must be nonnegative")
		}
		lo := int(start)
		if lo > len(r.published) {
			lo = len(r.published)
		}
		hi := len(r.published)
		if count != 0 && int64(lo)+count < int64(hi) {
			hi = lo + int(count)
		}
		values := make([]tjs.Value, 0, hi-lo)
		for _, c := range r.published[lo:hi] {
			if e := textRenderCharge(budget); e != nil {
				return tjs.VoidValue, e
			}
			x, e := textRenderCharacterValue(vm, c)
			if e != nil {
				return tjs.VoidValue, e
			}
			values = append(values, x)
		}
		return vm.NewArray(values)
	case "getKeyWait":
		values := make([]tjs.Value, 0, len(r.keyWaits))
		for _, p := range r.keyWaits {
			if e := textRenderCharge(budget); e != nil {
				return tjs.VoidValue, e
			}
			x, e := textRenderDictionary(vm, map[string]tjs.Value{"pos": tjs.IntegerValue(int64(p)), "time": tjs.RealValue(0)})
			if e != nil {
				return tjs.VoidValue, e
			}
			values = append(values, x)
		}
		return vm.NewArray(values)
	case "calcShowCount":
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		time, e := textRenderNumeric(v)
		if e != nil {
			return tjs.VoidValue, e
		}
		n := 0
		for _, c := range r.published {
			if c.delay*r.timeScale <= time {
				n++
			} else {
				break
			}
		}
		return tjs.IntegerValue(int64(n)), nil
	case "calcLineOffset":
		v, e := textRenderArg(args, 0, operation)
		if e != nil {
			return tjs.VoidValue, e
		}
		line, e := v.Int()
		if e != nil {
			return tjs.VoidValue, e
		}
		if line <= 0 {
			return tjs.RealValue(0), nil
		}
		if line >= int64(len(r.lines)) {
			return tjs.RealValue(float64(r.bottom)), nil
		}
		return tjs.RealValue(0), nil
	case "render":
		if r.busy {
			return tjs.VoidValue, fmt.Errorf("reentrant TextRender layout is not supported")
		}
		if !r.initialized {
			return tjs.VoidValue, fmt.Errorf("TextRender layout requires clear or setRenderSize")
		}
		r.busy = true
		v, e := s.textRenderRender(r, args, budget)
		r.busy = false
		return v, e
	case "newline":
		if !r.initialized {
			return tjs.VoidValue, fmt.Errorf("TextRender layout requires clear or setRenderSize")
		}
		return tjs.VoidValue, s.textRenderFinishLine(r, budget)
	case "done":
		if !r.initialized {
			return tjs.VoidValue, fmt.Errorf("TextRender layout requires clear or setRenderSize")
		}
		if e := s.textRenderFinishLine(r, budget); e != nil {
			return tjs.VoidValue, e
		}
		s.textRenderDone(r)
		return tjs.VoidValue, nil
	case "onEval":
		if context.Kind() != tjs.KindObject || context.IsNull() {
			return tjs.VoidValue, fmt.Errorf("TextRenderBase.onEval requires object context")
		}
		callback, e := tjs.NewDispatcher(vm.Objects).Get(context, "onEval")
		if e != nil {
			return tjs.VoidValue, e
		}
		if callback.IsVoid() {
			return tjs.VoidValue, fmt.Errorf("%w: TextRender onEval callback is absent", ErrUnsupportedNative)
		}
		value, e := vm.CallFunction(callback, context, args, s, budget)
		if e != nil {
			return tjs.VoidValue, e
		}
		return tjs.StringValue(value.Text()), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: TextRenderBase.%s", ErrUnsupportedNative, operation)
	}
}
func textRenderAttributes() map[string]string {
	return map[string]string{"Align": "align", "BigFontSize": "bigfontsize", "Bold": "bold", "ChColor": "color", "Edge": "edge", "EdgeColor": "edgecolor", "Face": "face", "FontSize": "fontsize", "Italic": "", "LineSize": "linesize", "LineSpacing": "linespacing", "Pitch": "pitch", "RubyOffset": "rubyoffset", "RubySize": "rubysize", "Shadow": "shadow", "ShadowColor": "shadowcolor", "ShadowDiff": "shadowdiff", "SmallFontSize": "smallfontsize", "Valign": "valign"}
}
func textRenderFieldKind(field string) byte { _, k, _ := textRenderField(field); return k }
