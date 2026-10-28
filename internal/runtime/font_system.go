package runtime

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

const (
	fixedFontFileName   = "NotoSansCJK-Regular.ttc"
	fixedFontFamilyName = "Noto Sans CJK SC"
)

// fixedFontPath returns the shared runtime asset path used by development
// runs and packaged binaries. The working-directory path supports go run;
// the executable-relative path supports packaged applications.
func fixedFontPath() (string, error) {
	const relativePath = "lib/fonts/" + fixedFontFileName
	var candidates []string
	if workingDir, err := os.Getwd(); err == nil {
		for dir := workingDir; ; dir = filepath.Dir(dir) {
			candidates = append(candidates, filepath.Join(dir, relativePath))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), relativePath))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: fixed font asset %s", ErrInvalidFont, relativePath)
}

func loadFixedFontData() ([]byte, error) {
	path, err := fixedFontPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidFont, path, err)
	}
	return data, nil
}

func fixedFontCandidates() []string {
	return []string{
		fixedFontFamilyName,
		"Noto Sans CJK JP",
		"Noto Sans CJK TC",
		"Noto Sans CJK HK",
		"Noto Sans CJK KR",
	}
}

func registerFixedFont(book *FontBook) error {
	if book == nil {
		return fmt.Errorf("%w: no font book", ErrInvalidFont)
	}
	if _, ok := book.lookup(fixedFontFamilyName); ok {
		return nil
	}
	data, err := loadFixedFontData()
	if err != nil {
		return err
	}
	_, err = registerFontData(book, data)
	return err
}

// registerFontData adds real faces from a font resource to a FontBook. The
// caller owns the byte slice; factories retain parsed font data as needed.
func registerFontData(book *FontBook, data []byte) (int, error) {
	if book == nil {
		return 0, fmt.Errorf("%w: no font book", ErrInvalidFont)
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("%w: empty font data", ErrInvalidFont)
	}
	collection, err := sfnt.ParseCollection(data)
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidFont, err)
	}
	type registration struct {
		family  string
		factory FontFactory
	}
	registrations := make([]registration, 0, collection.NumFonts())
	for i := range collection.NumFonts() {
		parsed, err := collection.Font(i)
		if err != nil {
			return 0, fmt.Errorf("%w: face %d: %w", ErrInvalidFont, i, err)
		}
		family, err := parsed.Name(nil, sfnt.NameIDTypographicFamily)
		if err != nil || strings.TrimSpace(family) == "" {
			family, err = parsed.Name(nil, sfnt.NameIDFamily)
		}
		family = strings.TrimSpace(family)
		if err != nil || family == "" {
			return 0, fmt.Errorf("%w: face %d has no Family name", ErrInvalidFont, i)
		}
		typo := parseTypoMetrics(data, i)
		registrations = append(registrations, registration{family: family, factory: fontFactoryWithTypo(parsed, typo)})
	}
	if len(registrations) == 0 {
		return 0, fmt.Errorf("%w: font contains no faces", ErrInvalidFont)
	}
	for _, registration := range registrations {
		if err := book.RegisterFactory(registration.family, registration.factory); err != nil {
			return 0, err
		}
	}
	return len(registrations), nil
}

func (s *Services) registerFontData(data []byte) (int, error) {
	if s == nil {
		return 0, fmt.Errorf("%w: no services", ErrInvalidFont)
	}
	s.ensureInit()
	s.mu.RLock()
	book := s.FontBook
	s.mu.RUnlock()
	return registerFontData(book, data)
}

type fontTypoMetrics struct {
	unitsPerEm     int
	sTypoAscender  int
	sTypoDescender int
	sTypoLineGap   int
	hasTypo        bool
}

type typoFace struct {
	font.Face
	metrics font.Metrics
}

func (f *typoFace) Metrics() font.Metrics {
	if f == nil {
		return font.Metrics{}
	}
	return f.metrics
}

func parseTypoMetrics(data []byte, fontIndex int) fontTypoMetrics {
	if len(data) < 12 {
		return fontTypoMetrics{}
	}
	var offset uint32
	if string(data[:4]) == "ttcf" {
		if len(data) < 12 {
			return fontTypoMetrics{}
		}
		numFonts := binary.BigEndian.Uint32(data[8:12])
		if fontIndex < 0 || uint32(fontIndex) >= numFonts {
			return fontTypoMetrics{}
		}
		entryOffset := 12 + uint32(fontIndex)*4
		if int(entryOffset+4) > len(data) {
			return fontTypoMetrics{}
		}
		offset = binary.BigEndian.Uint32(data[entryOffset : entryOffset+4])
	}
	if int(offset+12) > len(data) {
		return fontTypoMetrics{}
	}
	numTables := binary.BigEndian.Uint16(data[offset+4 : offset+6])
	var typo fontTypoMetrics
	var foundOS2, foundHead bool
	for i := 0; i < int(numTables); i++ {
		entry := offset + 12 + uint32(i*16)
		if int(entry+16) > len(data) {
			break
		}
		tag := string(data[entry : entry+4])
		tableOffset := binary.BigEndian.Uint32(data[entry+8 : entry+12])
		tableLen := binary.BigEndian.Uint32(data[entry+12 : entry+16])
		if tag == "OS/2" && int(tableOffset+tableLen) <= len(data) && tableLen >= 74 {
			typo.sTypoAscender = int(int16(binary.BigEndian.Uint16(data[tableOffset+68 : tableOffset+70])))
			typo.sTypoDescender = int(int16(binary.BigEndian.Uint16(data[tableOffset+70 : tableOffset+72])))
			typo.sTypoLineGap = int(int16(binary.BigEndian.Uint16(data[tableOffset+72 : tableOffset+74])))
			foundOS2 = true
		} else if tag == "head" && int(tableOffset+tableLen) <= len(data) && tableLen >= 20 {
			typo.unitsPerEm = int(binary.BigEndian.Uint16(data[tableOffset+18 : tableOffset+20]))
			foundHead = true
		}
		if foundOS2 && foundHead {
			break
		}
	}
	if foundOS2 && foundHead && typo.unitsPerEm > 0 && typo.sTypoAscender > 0 {
		typo.hasTypo = true
	}
	return typo
}

func fontFactoryWithTypo(parsed *sfnt.Font, typo fontTypoMetrics) FontFactory {
	return func(size float64) (FontFace, error) {
		if size <= 0 {
			return nil, fmt.Errorf("font size must be positive")
		}
		face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		if !typo.hasTypo || typo.unitsPerEm <= 0 || typo.sTypoAscender <= 0 {
			return face, nil
		}
		baseMetrics := face.Metrics()
		ascentPix := int(math.Round(size * float64(typo.sTypoAscender) / float64(typo.unitsPerEm)))
		heightPix := int(math.Round(size))
		descentPix := heightPix - ascentPix
		return &typoFace{
			Face: face,
			metrics: font.Metrics{
				Height:     fixed.I(heightPix),
				Ascent:     fixed.I(ascentPix),
				Descent:    fixed.I(descentPix),
				XHeight:    baseMetrics.XHeight,
				CapHeight:  baseMetrics.CapHeight,
				CaretSlope: baseMetrics.CaretSlope,
			},
		}, nil
	}
}

func fontFactoryFor(parsed *sfnt.Font) FontFactory {
	var typo fontTypoMetrics
	if parsed != nil {
		var buf bytes.Buffer
		if _, err := parsed.WriteSourceTo(nil, &buf); err == nil {
			typo = parseTypoMetrics(buf.Bytes(), 0)
		}
	}
	return fontFactoryWithTypo(parsed, typo)
}
