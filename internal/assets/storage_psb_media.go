package assets

import (
	"fmt"
	"strings"
)

// RegisterPSBMediaResources makes embedded resources available through
// psb://<container>/<resource> storage names.
func (s *Storage) RegisterPSBMediaResources(container string, resources map[string][]byte) error {
	if s == nil {
		return fmt.Errorf("nil storage")
	}
	domain := normalizePSBMediaDomain(container)
	if domain == "" || strings.IndexByte(domain, 0) >= 0 {
		return fmt.Errorf("invalid PSB media container")
	}

	s.psbMediaMu.Lock()
	if s.psbMediaDomains == nil {
		s.psbMediaDomains = make(map[string]struct{})
	}
	if s.psbMediaResources == nil {
		s.psbMediaResources = make(map[string][]byte, len(resources))
	}
	s.psbMediaDomains[domain] = struct{}{}
	for name, data := range resources {
		if name == "" || strings.IndexByte(name, 0) >= 0 {
			s.psbMediaMu.Unlock()
			return fmt.Errorf("invalid PSB media resource name")
		}
		s.psbMediaResources[domain+"/"+name] = data
	}
	s.psbMediaMu.Unlock()
	s.graphicCache.clear()
	return nil
}

func normalizePSBMediaDomain(domain string) string {
	domain = strings.ReplaceAll(domain, `\`, "/")
	if dot := strings.IndexByte(domain, '.'); dot >= 0 {
		domain = domain[:dot] + strings.ToLower(domain[dot:])
	}
	return domain
}

func (s *Storage) readPSBMedia(name string) ([]byte, bool, error) {
	_, data, found, handled, err := s.lookupPSBMedia(name)
	if !handled {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !found {
		return nil, true, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return append([]byte(nil), data...), true, nil
}

func (s *Storage) resolvePSBMedia(name string) (string, bool, bool, error) {
	resolved, _, found, handled, err := s.lookupPSBMedia(name)
	return resolved, found, handled, err
}

func (s *Storage) lookupPSBMedia(name string) (string, []byte, bool, bool, error) {
	separator := storageMediaSeparator(name)
	if separator < 0 || asciiLower(name[:separator]) != "psb" {
		return "", nil, false, false, nil
	}
	if s == nil {
		return "", nil, false, true, fmt.Errorf("nil storage")
	}
	uriPath := name[separator+1:]
	if !strings.HasPrefix(uriPath, "//") {
		return "", nil, false, true, fmt.Errorf("%w: malformed PSB storage name", ErrUnsupportedStorageMedia)
	}
	uriPath = strings.ReplaceAll(uriPath[2:], `\`, "/")

	s.psbMediaMu.RLock()
	defer s.psbMediaMu.RUnlock()
	resolved := ""
	var data []byte
	for domain := range s.psbMediaDomains {
		if len(uriPath) <= len(domain) || uriPath[len(domain)] != '/' ||
			normalizePSBMediaDomain(uriPath[:len(domain)]) != domain {
			continue
		}
		resource := uriPath[len(domain)+1:]
		if resource == "" {
			continue
		}
		if candidate, ok := s.psbMediaResources[domain+"/"+resource]; ok && len(domain) > len(resolved) {
			resolved = domain
			data = candidate
		}
	}
	if resolved == "" {
		return "", nil, false, true, nil
	}
	resource := uriPath[len(resolved)+1:]
	return "psb://" + resolved + "/" + resource, data, true, true, nil
}
