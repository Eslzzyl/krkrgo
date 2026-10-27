package runtime

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

var (
	defaultFontsOnce  sync.Once
	defaultFonts      []SystemFont
	defaultFontErrors []error
)

// SystemFont is a real OpenType face found in a font directory.
type SystemFont struct {
	Family  string
	Factory FontFactory
	Path    string
}

var (
	darwinSystemFontFallbackFamilies = [...]string{
		"Hiragino Sans",
		"Hiragino Kaku Gothic ProN",
		"Hiragino Mincho ProN",
		"Hiragino Maru Gothic ProN",
	}
	darwinSystemSerifFontFallbackFamilies = [...]string{
		"Hiragino Mincho ProN",
		"Hiragino Mincho Pro",
		"Yu Mincho",
		"Hiragino Sans",
		"Hiragino Kaku Gothic ProN",
		"Hiragino Maru Gothic ProN",
	}
	iosSystemCJKFallbackFamilies = [...]string{
		"PingFang SC",
		"PingFang TC",
		"PingFang HK",
	}
	darwinSystemCJKFallbackFamilies = [...]string{
		"PingFang SC",
		"PingFang TC",
		"PingFang HK",
		"Hiragino Sans GB",
		"Hiragino Sans",
		"STHeiti",
		"Heiti SC",
		"Songti SC",
		"Noto Sans CJK SC",
		"Noto Sans CJK JP",
	}
	linuxSystemCJKFallbackFamilies = [...]string{
		"Noto Sans CJK SC",
		"Noto Sans CJK TC",
		"Noto Sans CJK JP",
		"Source Han Sans SC",
		"Source Han Sans CN",
		"WenQuanYi Zen Hei",
		"WenQuanYi Micro Hei",
		"Droid Sans Fallback",
		"AR PL UMing CN",
	}
	linuxSystemFontFallbackFamilies = [...]string{
		"Noto Sans CJK JP",
		"Noto Sans JP",
		"Noto Serif CJK JP",
		"Noto Serif JP",
		"IPAexGothic",
		"IPAGothic",
		"VL Gothic",
		"TakaoPGothic",
		"Droid Sans Japanese",
	}
	linuxSystemSerifFontFallbackFamilies = [...]string{
		"Noto Serif CJK SC",
		"Noto Serif CJK TC",
		"Noto Serif CJK JP",
		"Source Han Serif SC",
		"Source Han Serif CN",
		"AR PL UMing CN",
		"AR PL KaitiM GB",
	}
	windowsSystemCJKFallbackFamilies = [...]string{
		"Microsoft YaHei UI",
		"Microsoft YaHei",
		"DengXian",
		"SimSun",
		"NSimSun",
		"SimHei",
		"Microsoft JhengHei UI",
		"Yu Gothic UI",
		"Yu Gothic",
		"Meiryo",
		"MS Gothic",
		"MS PGothic",
	}
	windowsSystemFontFallbackFamilies = [...]string{
		"Yu Gothic UI",
		"Yu Gothic",
		"Meiryo",
		"Meiryo UI",
		"MS PGothic",
		"MS Gothic",
		"ＭＳ Ｐゴシック",
		"ＭＳ ゴシック",
	}
	windowsSystemSerifFontFallbackFamilies = [...]string{
		"MS PMincho",
		"ＭＳ Ｐ明朝",
		"MS Mincho",
		"ＭＳ 明朝",
		"Yu Mincho",
		"Yu Mincho Demibold",
		"Yu Gothic",
		"Meiryo",
		"MS Gothic",
	}
	androidSystemCJKFallbackFamilies = [...]string{
		"Noto Sans CJK",
		"Noto Sans",
		"Droid Sans Fallback",
	}
)

func systemCJKFallbackCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return darwinSystemCJKFallbackFamilies[:]
	case "ios":
		return iosSystemCJKFallbackFamilies[:]
	case "linux":
		return linuxSystemCJKFallbackFamilies[:]
	case "windows":
		return windowsSystemCJKFallbackFamilies[:]
	case "android":
		return androidSystemCJKFallbackFamilies[:]
	default:
		return nil
	}
}

// systemFontFallbackCandidates lists common Latin/Japanese-capable families.
// Selection still requires an exact registered family name from FontBook.
func systemFontFallbackCandidates(requested string) []string {
	// CJK font family requests carry meaningful face intent. Preserve Mincho/
	// serif requests instead of silently substituting the first available sans
	// family (notably MS P Mincho on macOS).
	normalized := fontKey(requested)
	serif := strings.Contains(normalized, "mincho") ||
		strings.Contains(normalized, "明朝") ||
		strings.Contains(normalized, "serif")
	switch runtime.GOOS {
	case "darwin":
		if serif {
			return darwinSystemSerifFontFallbackFamilies[:]
		}
		return darwinSystemFontFallbackFamilies[:]
	case "linux":
		if serif {
			return linuxSystemSerifFontFallbackFamilies[:]
		}
		return linuxSystemFontFallbackFamilies[:]
	case "windows":
		if serif {
			return windowsSystemSerifFontFallbackFamilies[:]
		}
		return windowsSystemFontFallbackFamilies[:]
	default:
		return nil
	}
}

// SystemFontDirectories returns conventional font directories for the current
// platform. It does not require those directories to exist.
func SystemFontDirectories() []string {
	home, _ := os.UserHomeDir()
	var dirs []string
	add := func(values ...string) {
		for _, value := range values {
			if value != "" {
				dirs = append(dirs, filepath.Clean(value))
			}
		}
	}
	switch runtime.GOOS {
	case "darwin":
		add("/System/Library/Fonts", "/Library/Fonts")
		if home != "" {
			add(filepath.Join(home, "Library", "Fonts"))
		}
	case "windows":
		add(os.Getenv("WINDIR") + `\Fonts`)
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			add(filepath.Join(appData, "Microsoft", "Windows", "Fonts"))
		}
	case "linux":
		add("/usr/share/fonts", "/usr/local/share/fonts")
		if home != "" {
			add(filepath.Join(home, ".fonts"), filepath.Join(home, ".local", "share", "fonts"))
		}
	}
	return uniquePaths(dirs)
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		key := strings.ToLower(filepath.Clean(path))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, filepath.Clean(path))
	}
	return result
}

// ScanSystemFonts recursively scans the supplied roots in lexical path order.
// Roots may be injected by tests or applications. For duplicate family names,
// the first valid face wins; collection faces are considered in file order.
// Invalid font files are skipped and returned as diagnosable ErrInvalidFont
// errors, alongside any successfully discovered faces.
func ScanSystemFonts(roots ...string) ([]SystemFont, []error) {
	var paths []string
	var errs []error
	for _, root := range uniquePaths(roots) {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				errs = append(errs, fmt.Errorf("scan %s: %w", path, err))
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".ttf", ".otf", ".ttc", ".otc", ".dfont":
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("scan %s: %w", root, err))
		}
	}
	sort.Strings(paths)
	seen := make(map[string]struct{})
	fonts := make([]SystemFont, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: %w", ErrInvalidFont, path, err))
			continue
		}
		collection, err := sfnt.ParseCollection(data)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %s: %w", ErrInvalidFont, path, err))
			continue
		}
		for index := 0; index < collection.NumFonts(); index++ {
			parsed, err := collection.Font(index)
			if err != nil {
				errs = append(errs, fmt.Errorf("%w: %s face %d: %w", ErrInvalidFont, path, index, err))
				continue
			}
			family, err := parsed.Name(nil, sfnt.NameIDTypographicFamily)
			if err != nil || strings.TrimSpace(family) == "" {
				family, err = parsed.Name(nil, sfnt.NameIDFamily)
			}
			family = strings.TrimSpace(family)
			if err != nil || family == "" {
				errs = append(errs, fmt.Errorf("%w: %s face %d has no Family name", ErrInvalidFont, path, index))
				continue
			}
			key := fontKey(family)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			fonts = append(fonts, SystemFont{Family: family, Path: path, Factory: fontFactoryForPath(path, index)})
		}
	}
	return fonts, errs
}

// registerFontData adds real faces from a storage resource to the runtime font
// book. It makes them available to Font.getList and font selection without
// installing fonts in the host operating system.
func (s *Services) registerFontData(data []byte) (int, error) {
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
		if err := s.RegisterFontFactory(registration.family, registration.factory); err != nil {
			return 0, err
		}
	}
	return len(registrations), nil
}

func fontFactoryFor(parsed *sfnt.Font) FontFactory {
	return func(size float64) (FontFace, error) {
		if size <= 0 {
			return nil, fmt.Errorf("font size must be positive")
		}
		return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
}

// fontFactoryForPath defers reading and parsing a system font until a face is
// requested. ScanSystemFonts keeps only path and face index, so unused system
// font data is released after discovery.
func fontFactoryForPath(path string, index int) FontFactory {
	var (
		once   sync.Once
		parsed *sfnt.Font
		err    error
	)
	return func(size float64) (FontFace, error) {
		if size <= 0 {
			return nil, fmt.Errorf("font size must be positive")
		}
		once.Do(func() {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				err = readErr
				return
			}
			collection, parseErr := sfnt.ParseCollection(data)
			if parseErr != nil {
				err = parseErr
				return
			}
			if index < 0 || index >= collection.NumFonts() {
				err = fmt.Errorf("%w: %s face %d is out of range", ErrInvalidFont, path, index)
				return
			}
			parsed, err = collection.Font(index)
		})
		if err != nil {
			return nil, err
		}
		return opentype.NewFace(parsed, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	}
}

func (b *FontBook) DiscoverSystemFonts() []error {
	defaultFontsOnce.Do(func() {
		defaultFonts, defaultFontErrors = ScanSystemFonts(SystemFontDirectories()...)
	})
	b.registerDiscoveredFonts(defaultFonts)
	return append([]error(nil), defaultFontErrors...)
}

func (b *FontBook) registerDiscoveredFonts(fonts []SystemFont) {
	if b == nil {
		return
	}
	for _, discovered := range fonts {
		b.mu.Lock()
		if b.entries == nil {
			b.entries = make(map[string]fontEntry)
		}
		key := fontKey(discovered.Family)
		if _, exists := b.entries[key]; !exists {
			b.entries[key] = fontEntry{name: discovered.Family, factory: discovered.Factory, discovered: true}
			b.order = append(b.order, key)
		}
		b.mu.Unlock()
	}
}

// RegisterSystemFonts adds discovered system fonts without replacing existing
// entries. Explicit registrations therefore retain priority.
func (b *FontBook) RegisterSystemFonts(roots ...string) []error {
	if b == nil {
		return []error{fmt.Errorf("%w: no font book", ErrInvalidFont)}
	}
	fonts, errs := ScanSystemFonts(roots...)
	b.registerDiscoveredFonts(fonts)
	return errs
}
