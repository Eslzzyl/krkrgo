package assets

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type psdMediaDocument struct {
	document     *PSD
	lazy         bool
	layersByPath map[string]map[string]int
	layersByID   map[int32]int
}

// RegisterPSDMedia exposes a loaded PSD's normal layers through psd:// media.
// The basename alias matches the PSD helper scripts' extractStorageName use;
// the C++ plugin's post-first-separator domain is retained as well.
func (s *Storage) RegisterPSDMedia(storageName string, document *PSD) error {
	return s.registerPSDMedia(storageName, document, false)
}

func (s *Storage) registerPSDMedia(storageName string, document *PSD, lazy bool) error {
	if s == nil {
		return fmt.Errorf("nil storage")
	}
	if document == nil {
		return fmt.Errorf("nil PSD document")
	}
	domains := psdMediaDomainsForName(storageName)
	if len(domains) == 0 {
		return fmt.Errorf("empty PSD media domain")
	}
	media := &psdMediaDocument{
		document:     document,
		lazy:         lazy,
		layersByPath: make(map[string]map[string]int),
		layersByID:   make(map[int32]int),
	}
	for index := len(document.Layers) - 1; index >= 0; index-- {
		layer := &document.Layers[index]
		if layer.Type != PSDLayerNormal {
			continue
		}
		pathname := psdMediaLayerPath(document, layer)
		name := psdMediaLayerName(layer.Name)
		names := media.layersByPath[pathname]
		if names == nil {
			names = make(map[string]int)
			media.layersByPath[pathname] = names
		}
		names[name] = index
		media.layersByID[layer.LayerID] = index
	}

	s.psdMediaMu.Lock()
	if s.psdMediaDocuments == nil {
		s.psdMediaDocuments = make(map[string]*psdMediaDocument)
	}
	for _, domain := range domains {
		s.psdMediaDocuments[domain] = media
	}
	s.psdMediaMu.Unlock()
	s.graphicCache.clear()
	return nil
}

// ClearPSDMediaCache drops PSD documents loaded lazily by psd:// lookups.
func (s *Storage) ClearPSDMediaCache() {
	if s == nil {
		return
	}
	s.psdMediaMu.Lock()
	for domain, media := range s.psdMediaDocuments {
		if media.lazy {
			delete(s.psdMediaDocuments, domain)
		}
	}
	s.psdMediaMu.Unlock()
	s.graphicCache.clear()
}

// UnregisterPSDMedia removes domains that still refer to document. A newer
// document loaded from the same filename remains available.
func (s *Storage) UnregisterPSDMedia(document *PSD) {
	if s == nil || document == nil {
		return
	}
	s.psdMediaMu.Lock()
	for domain, media := range s.psdMediaDocuments {
		if media.document == document {
			delete(s.psdMediaDocuments, domain)
		}
	}
	s.psdMediaMu.Unlock()
	s.graphicCache.clear()
}

func psdMediaDomainsForName(name string) []string {
	name = strings.ReplaceAll(name, `\`, "/")
	domain := name
	if slash := strings.IndexByte(name, '/'); slash >= 0 {
		domain = name[slash+1:]
	}
	basename := name
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '/', '>', '\\':
			basename = name[i+1:]
			i = 0
		}
	}
	domains := make([]string, 0, 2)
	for _, candidate := range []string{domain, basename} {
		candidate = strings.ToLower(candidate)
		if candidate == "" || strings.IndexByte(candidate, 0) >= 0 {
			continue
		}
		duplicate := false
		for _, registered := range domains {
			duplicate = duplicate || registered == candidate
		}
		if !duplicate {
			domains = append(domains, candidate)
		}
	}
	return domains
}

func psdMediaLayerName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "/", "_"))
}

func psdMediaLayerPath(document *PSD, layer *PSDLayer) string {
	parents := make([]string, 0, 4)
	parent := layer.ParentIndex
	for parent >= 0 && parent < len(document.Layers) {
		parents = append(parents, psdMediaLayerName(document.Layers[parent].Name))
		parent = document.Layers[parent].ParentIndex
	}
	var path strings.Builder
	path.WriteString("root/")
	for index := len(parents) - 1; index >= 0; index-- {
		path.WriteString(parents[index])
		path.WriteByte('/')
	}
	return path.String()
}

func parsePSDMediaName(name string) (domain, resource string, handled bool, err error) {
	separator := storageMediaSeparator(name)
	if separator < 0 || asciiLower(name[:separator]) != "psd" {
		return "", "", false, nil
	}
	if name == "" {
		return "", "", true, fmt.Errorf("%w: malformed PSD storage name", ErrUnsupportedStorageMedia)
	}
	uriPath := strings.ReplaceAll(name[separator+1:], `\`, "/")
	if !strings.HasPrefix(uriPath, "//") {
		return "", "", true, fmt.Errorf("%w: malformed PSD storage name", ErrUnsupportedStorageMedia)
	}
	uriPath = strings.ToLower(uriPath[2:])
	slash := strings.IndexByte(uriPath, '/')
	if slash <= 0 {
		return "", "", true, fmt.Errorf("%w: malformed PSD storage name", ErrUnsupportedStorageMedia)
	}
	return uriPath[:slash], uriPath[slash+1:], true, nil
}

func (s *Storage) lookupPSDMedia(name string) (string, *PSD, int, bool, bool, error) {
	domain, resource, handled, err := parsePSDMediaName(name)
	if !handled || err != nil {
		return "", nil, 0, false, handled, err
	}
	if s == nil {
		return "", nil, 0, false, true, fmt.Errorf("nil storage")
	}
	s.psdMediaMu.RLock()
	media := s.psdMediaDocuments[domain]
	s.psdMediaMu.RUnlock()
	if media == nil {
		var err error
		media, err = s.loadPSDMediaDomain(domain)
		if err != nil {
			return "", nil, 0, false, true, err
		}
	}
	if media == nil {
		return "", nil, 0, false, true, nil
	}
	index, found := media.lookupLayer(resource)
	if !found {
		return "", nil, 0, false, true, nil
	}
	return "psd://" + domain + "/" + resource, media.document, index, true, true, nil
}

func (s *Storage) loadPSDMediaDomain(domain string) (*psdMediaDocument, error) {
	s.psdMediaLoadMu.Lock()
	defer s.psdMediaLoadMu.Unlock()
	s.psdMediaMu.RLock()
	media := s.psdMediaDocuments[domain]
	s.psdMediaMu.RUnlock()
	if media != nil {
		return media, nil
	}
	data, err := s.Read(domain)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	document, err := ParsePSD(data)
	if err != nil {
		return nil, err
	}
	if err := s.registerPSDMedia(domain, document, true); err != nil {
		return nil, err
	}
	s.psdMediaMu.RLock()
	media = s.psdMediaDocuments[domain]
	s.psdMediaMu.RUnlock()
	return media, nil
}

func (media *psdMediaDocument) lookupLayer(resource string) (int, bool) {
	if strings.HasPrefix(resource, "id/") {
		name := resource[3:]
		if strings.ContainsRune(name, '/') {
			return 0, false
		}
		base, ok := psdBMPBaseName(name)
		if !ok || base == "" {
			return 0, false
		}
		for _, digit := range base {
			if digit < '0' || digit > '9' {
				return 0, false
			}
		}
		id, err := strconv.ParseInt(base, 10, 32)
		if err != nil {
			return 0, false
		}
		index, found := media.layersByID[int32(id)]
		return index, found
	}
	slash := strings.LastIndexByte(resource, '/')
	if slash < 0 {
		return 0, false
	}
	base, ok := psdBMPBaseName(resource[slash+1:])
	if !ok {
		return 0, false
	}
	names := media.layersByPath[resource[:slash+1]]
	index, found := names[base]
	return index, found
}

func psdBMPBaseName(name string) (string, bool) {
	dot := strings.IndexByte(name, '.')
	if dot < 0 || name[dot:] != ".bmp" {
		return "", false
	}
	return name[:dot], true
}

func (s *Storage) resolvePSDMedia(name string) (string, bool, bool, error) {
	resolved, _, _, found, handled, err := s.lookupPSDMedia(name)
	return resolved, found, handled, err
}

func (s *Storage) readPSDMedia(name string) ([]byte, bool, error) {
	resolved, document, index, found, handled, err := s.lookupPSDMedia(name)
	if !handled {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !found {
		return nil, true, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	data, err := psdLayerBMP(document, index)
	if errors.Is(err, ErrPSDEmptyLayer) {
		return nil, true, fmt.Errorf("%w: %s", ErrNotFound, resolved)
	}
	return data, true, err
}

func psdLayerBMP(document *PSD, index int) ([]byte, error) {
	image, err := document.LayerImage(index, PSDImageMasked)
	if err != nil {
		return nil, err
	}
	width, height := int(image.Width), int(image.Height)
	if width <= 0 || height <= 0 {
		return nil, ErrPSDEmptyLayer
	}
	maxInt := int(^uint(0) >> 1)
	if width > (maxInt-54)/4/height {
		return nil, fmt.Errorf("PSD layer BMP exceeds addressable size")
	}
	pixelBytes := width * height * 4
	if len(image.RGBA) != pixelBytes || uint64(pixelBytes) > uint64(^uint32(0))-54 {
		return nil, fmt.Errorf("invalid PSD layer image buffer")
	}
	data := make([]byte, 54+pixelBytes)
	copy(data[:2], "BM")
	binary.LittleEndian.PutUint32(data[2:6], uint32(len(data)))
	binary.LittleEndian.PutUint32(data[10:14], 54)
	binary.LittleEndian.PutUint32(data[14:18], 40)
	binary.LittleEndian.PutUint32(data[18:22], uint32(width))
	binary.LittleEndian.PutUint32(data[22:26], uint32(height))
	binary.LittleEndian.PutUint16(data[26:28], 1)
	binary.LittleEndian.PutUint16(data[28:30], 32)
	for y := 0; y < height; y++ {
		sourceRow := (height - 1 - y) * width * 4
		targetRow := 54 + y*width*4
		for x := 0; x < width; x++ {
			source := sourceRow + x*4
			target := targetRow + x*4
			data[target] = image.RGBA[source+2]
			data[target+1] = image.RGBA[source+1]
			data[target+2] = image.RGBA[source]
			data[target+3] = image.RGBA[source+3]
		}
	}
	return data, nil
}

func (s *Storage) readPSDMediaDir(name string) ([]string, bool, bool, error) {
	domain, resource, handled, err := parsePSDMediaName(name)
	if !handled || err != nil {
		return nil, false, handled, err
	}
	if s == nil {
		return nil, false, true, fmt.Errorf("nil storage")
	}
	s.psdMediaMu.RLock()
	media := s.psdMediaDocuments[domain]
	s.psdMediaMu.RUnlock()
	if media == nil {
		media, err = s.loadPSDMediaDomain(domain)
		if err != nil {
			return nil, false, true, err
		}
	}
	if media == nil {
		return nil, false, true, nil
	}
	if resource == "id/" {
		ids := make([]int, 0, len(media.layersByID))
		for id := range media.layersByID {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			names = append(names, strconv.Itoa(id)+".bmp")
		}
		return names, true, true, nil
	}
	byName, found := media.layersByPath[resource]
	if !found {
		return nil, false, true, nil
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name+".bmp")
	}
	sort.Strings(names)
	return names, true, true, nil
}
