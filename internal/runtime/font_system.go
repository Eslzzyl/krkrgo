package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
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
		registrations = append(registrations, registration{family: family, factory: fontFactoryFor(parsed)})
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

func fontFactoryFor(parsed *sfnt.Font) FontFactory {
	return func(size float64) (FontFace, error) {
		if size <= 0 {
			return nil, fmt.Errorf("font size must be positive")
		}
		return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
}
