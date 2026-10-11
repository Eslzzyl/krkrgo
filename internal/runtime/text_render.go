package runtime

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

const (
	WrapNone WrapMode = iota
	WrapCharacter
	WrapWord
)

type WrapMode uint8

// TextLayoutOptions controls deterministic line layout. MaxWidth <= 0 disables
// wrapping. Kinsoku defaults to true in LayoutTextWithOptions.
type TextLayoutOptions struct {
	MaxWidth   int
	Mode       WrapMode
	Kinsoku    bool
	LineHeight int
}

// TextLine is a laid-out line. UTF-16 offsets are code-unit offsets, matching
// TJS strings; EndUTF16 excludes a line-break code unit.
type TextLine struct {
	Text       string
	StartUTF16 int
	EndUTF16   int
	Width      int
}

// TextLayout contains bounded, headless layout output.
type TextLayout struct {
	Lines       []TextLine
	Width       int
	Height      int
	LineHeight  int
	UTF16Length int
}

// TextMetrics describes unwrapped text dimensions and UTF-16 length.
type TextMetrics struct {
	Width       int
	Height      int
	LineHeight  int
	Lines       int
	UTF16Length int
}

// TextRender renders a selected face into an RGBA image. It deliberately has
// no Ebitengine dependency; callers may upload the resulting pixels separately.
type TextRender struct {
	Face       FontFace
	LineHeight int
	Color      color.Color
}

// TextRenderer is the descriptive alias used by callers that prefer the noun.
type TextRenderer = TextRender

func NewTextRender(face FontFace) (*TextRender, error) {
	if face == nil {
		return nil, fmt.Errorf("%w: nil face", ErrUnsupportedFont)
	}
	metrics, err := MetricsForFace(face)
	if err != nil {
		return nil, err
	}
	return &TextRender{Face: face, LineHeight: metrics.Height, Color: color.White}, nil
}

func NewTextRenderer(face FontFace) (*TextRender, error) { return NewTextRender(face) }

// MeasureText measures explicit newlines but does not wrap. Width includes
// kerning and glyph advances, and is always deterministic for a given face.
func MeasureText(face FontFace, text string) (TextMetrics, error) {
	layout, err := LayoutTextWithOptions(face, text, TextLayoutOptions{Mode: WrapNone, Kinsoku: true})
	if err != nil {
		return TextMetrics{}, err
	}
	return TextMetrics{Width: layout.Width, Height: layout.Height, LineHeight: layout.LineHeight, Lines: len(layout.Lines), UTF16Length: layout.UTF16Length}, nil
}

func (r *TextRender) Measure(text string) (TextMetrics, error) {
	if r == nil {
		return TextMetrics{}, fmt.Errorf("%w: nil renderer", ErrInvalidFont)
	}
	return MeasureText(r.Face, text)
}

func LayoutText(face FontFace, text string, maxWidth int) (TextLayout, error) {
	mode := WrapNone
	if maxWidth > 0 {
		mode = WrapCharacter
	}
	return LayoutTextWithOptions(face, text, TextLayoutOptions{MaxWidth: maxWidth, Mode: mode, Kinsoku: true})
}

// LayoutTextWithOptions performs line wrapping while retaining UTF-16 offsets.
// The implementation intentionally keeps shaping at the x/image/font boundary:
// each glyph's real advance and kerning are used, and missing glyphs are
// reported as unsupported instead of being replaced with invented pixels.
func LayoutTextWithOptions(face FontFace, text string, options TextLayoutOptions) (TextLayout, error) {
	if face == nil {
		return TextLayout{}, fmt.Errorf("%w: nil face", ErrUnsupportedFont)
	}
	if len(text) > maxTextBytes {
		return TextLayout{}, fmt.Errorf("%w: text exceeds %d bytes", ErrInvalidFont, maxTextBytes)
	}
	metrics, err := MetricsForFace(face)
	if err != nil {
		return TextLayout{}, err
	}
	lineHeight := options.LineHeight
	if lineHeight <= 0 {
		lineHeight = metrics.Height
	}
	if lineHeight > maxTextDimension {
		return TextLayout{}, fmt.Errorf("%w: line height exceeds bound", ErrInvalidFont)
	}
	units := utf16.Encode([]rune(text))
	if len(units) > maxUTF16Units {
		return TextLayout{}, fmt.Errorf("%w: text exceeds UTF-16 bound", ErrInvalidFont)
	}
	items := make([]layoutRune, 0, len([]rune(text)))
	unitOffset := 0
	for _, rr := range text {
		n := 1
		if rr > 0xffff {
			n = 2
		}
		if rr == '\r' || rr == '\n' {
			items = append(items, layoutRune{r: rr, start: unitOffset, end: unitOffset + n, newline: true})
			unitOffset += n
			continue
		}
		advance, ok := face.GlyphAdvance(rr)
		if !ok {
			return TextLayout{}, fmt.Errorf("%w: glyph U+%04X in selected face", ErrUnsupportedFont, rr)
		}
		items = append(items, layoutRune{r: rr, start: unitOffset, end: unitOffset + n, advance: advance})
		unitOffset += n
	}

	lines := make([]TextLine, 0, 1)
	start := 0
	for start < len(items) {
		// Explicit line breaks produce an empty line and consume CRLF as one
		// logical break while preserving both UTF-16 code units in the total.
		if items[start].newline {
			lineEnd := items[start].start
			lines = append(lines, TextLine{StartUTF16: lineEnd, EndUTF16: lineEnd})
			start++
			if items[start-1].r == '\r' && start < len(items) && items[start].r == '\n' {
				start++
			}
			if start == len(items) {
				lines = append(lines, TextLine{StartUTF16: unitOffset, EndUTF16: unitOffset})
			}
			continue
		}

		end, width := chooseLine(face, items, start, options)
		if end <= start { // Defensive progress for a single over-wide glyph.
			end = start + 1
			width = fixedToPixels(items[start].advance)
		}
		lineStart := items[start].start
		lineEnd := items[end-1].end
		lineText := textFromUTF16Units(units[lineStart:lineEnd])
		lines = append(lines, TextLine{Text: lineText, StartUTF16: lineStart, EndUTF16: lineEnd, Width: width})
		start = end
		if start < len(items) && items[start].newline {
			start++
			if items[start-1].r == '\r' && start < len(items) && items[start].r == '\n' {
				start++
			}
			if start == len(items) {
				lines = append(lines, TextLine{StartUTF16: unitOffset, EndUTF16: unitOffset})
			}
		}
	}
	if len(lines) == 0 {
		lines = append(lines, TextLine{})
	}
	width := 0
	for i := range lines {
		if lines[i].Width > width {
			width = lines[i].Width
		}
	}
	if width > maxTextDimension {
		return TextLayout{}, fmt.Errorf("%w: layout width exceeds bound", ErrInvalidFont)
	}
	height64 := int64(lineHeight) * int64(len(lines))
	if height64 > maxTextPixels {
		return TextLayout{}, fmt.Errorf("%w: layout exceeds bound", ErrInvalidFont)
	}
	return TextLayout{Lines: lines, Width: width, Height: int(height64), LineHeight: lineHeight, UTF16Length: unitOffset}, nil
}

func (r *TextRender) Layout(text string, options TextLayoutOptions) (TextLayout, error) {
	if r == nil {
		return TextLayout{}, fmt.Errorf("%w: nil renderer", ErrInvalidFont)
	}
	if options.LineHeight <= 0 {
		options.LineHeight = r.LineHeight
	}
	return LayoutTextWithOptions(r.Face, text, options)
}

// RasterizeText returns tightly sized RGBA pixels with transparent background.
// Glyph masks come directly from the selected face through font.Drawer.
func RasterizeText(face FontFace, text string, foreground color.Color) (*image.RGBA, error) {
	layout, err := LayoutText(face, text, 0)
	if err != nil {
		return nil, err
	}
	return rasterizeLayout(face, layout, foreground, nil)
}

// RasterizeTextWithOptions combines layout and real-face rasterization.
func RasterizeTextWithOptions(face FontFace, text string, foreground color.Color, options TextLayoutOptions) (*image.RGBA, error) {
	layout, err := LayoutTextWithOptions(face, text, options)
	if err != nil {
		return nil, err
	}
	return rasterizeLayout(face, layout, foreground, nil)
}

func (r *TextRender) Rasterize(text string) (*image.RGBA, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil renderer", ErrInvalidFont)
	}
	fg := r.Color
	if fg == nil {
		fg = color.White
	}
	return RasterizeTextWithOptions(r.Face, text, fg, TextLayoutOptions{LineHeight: r.LineHeight, Kinsoku: true})
}

func rasterizeLayout(face FontFace, layout TextLayout, foreground color.Color, background color.Color) (*image.RGBA, error) {
	if layout.Width < 0 || layout.Width > maxTextDimension || layout.Height < 0 || layout.Height > maxTextDimension || int64(max(1, layout.Width))*int64(max(1, layout.Height)) > maxTextPixels {
		return nil, fmt.Errorf("%w: raster dimensions exceed bound", ErrInvalidFont)
	}
	w, h := layout.Width, layout.Height
	if w == 0 {
		w = 1
	}
	if h == 0 {
		h = max(1, layout.LineHeight)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if background != nil {
		draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	}
	metrics := face.Metrics()
	fg := foreground
	if fg == nil {
		fg = color.White
	}
	for i, line := range layout.Lines {
		baseline := i*layout.LineHeight + fixedToPixels(metrics.Ascent)
		d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face, Dot: fixed.P(0, baseline)}
		d.DrawString(line.Text)
	}
	return img, nil
}

const (
	maxTextBytes     = 1 << 20
	maxUTF16Units    = 1 << 20
	maxTextDimension = 1 << 15
	maxTextPixels    = 1 << 26
)

type layoutRune struct {
	r          rune
	start, end int
	advance    fixed.Int26_6
	newline    bool
}

func chooseLine(face FontFace, items []layoutRune, start int, options TextLayoutOptions) (int, int) {
	if options.MaxWidth <= 0 || options.Mode == WrapNone {
		end := start
		width := 0
		var prev rune
		for end < len(items) && !items[end].newline {
			width += advancePixels(face, prev, items[end])
			prev = items[end].r
			end++
		}
		return end, width
	}
	width := 0
	end := start
	var prev rune
	lastWord := -1
	for end < len(items) && !items[end].newline {
		item := items[end]
		next := width + advancePixels(face, prev, item)
		if options.Mode == WrapWord && isBreakSpace(item.r) {
			lastWord = end + 1
		}
		if end > start && next > options.MaxWidth {
			cut := end
			if options.Mode == WrapWord && lastWord > start {
				cut = lastWord
				for cut < len(items) && isBreakSpace(items[cut].r) {
					cut++
				}
			}
			if options.Kinsoku {
				cut = adjustKinsoku(items, start, cut)
			}
			if cut > start {
				return cut, measureItems(face, items[start:cut])
			}
		}
		width = next
		prev = item.r
		end++
	}
	return end, width
}

func measureItems(face FontFace, items []layoutRune) int {
	width := 0
	var prev rune
	for _, item := range items {
		width += advancePixels(face, prev, item)
		prev = item.r
	}
	return width
}

func advancePixels(face FontFace, prev rune, item layoutRune) int {
	v := item.advance
	if prev != 0 {
		v += face.Kern(prev, item.r)
	}
	return fixedToPixels(v)
}

func adjustKinsoku(items []layoutRune, start, cut int) int {
	if cut <= start || cut > len(items) {
		return cut
	}
	// Avoid ending a line with opening punctuation. Move it to the next line.
	for cut > start && isKinsokuLineEnd(items[cut-1].r) {
		cut--
	}
	// Avoid a line beginning with closing punctuation by keeping that rune on
	// the previous line. This may exceed MaxWidth for an unavoidable pair.
	if cut < len(items) && isKinsokuLineStart(items[cut].r) && cut > start {
		cut++
	}
	return cut
}

func isBreakSpace(r rune) bool { return r == ' ' || r == '\t' || unicode.IsSpace(r) }

func isKinsokuLineStart(r rune) bool {
	return strings.ContainsRune("、。，．！？：；》」』】〕〉》〕）］｝〕〉’”)]}!?,.;:", r)
}
func isKinsokuLineEnd(r rune) bool {
	return strings.ContainsRune("（［｛「『【〔〈《〖〘〝‘“([{<", r)
}

func fixedToPixels(v fixed.Int26_6) int {
	if v <= 0 {
		return 0
	}
	return int((int64(v) + 63) / 64)
}

func textFromUTF16Units(units []uint16) string { return string(utf16.Decode(units)) }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
