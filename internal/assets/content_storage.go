package assets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/core"
)

// ContentFile is a random-access, read-only file supplied by a platform
// storage provider.
type ContentFile interface {
	io.ReaderAt
	io.Closer
}

// ContentEntry is the small metadata set needed by the storage runtime.
type ContentEntry struct {
	Name    string
	IsDir   bool
	Size    int64
	Mode    os.FileMode
	ModTime int64
	Atime   int64
	Ctime   int64
}

// ContentBackend supplies a project tree without exposing an operating-system
// path. Paths are project-relative and use forward slashes.
type ContentBackend interface {
	Open(path string) (ContentFile, int64, error)
	ReadDir(path string) ([]ContentEntry, error)
	Stat(path string) (ContentEntry, error)
}

// OpenContentStorage creates a read-only storage over a platform URI tree.
// The backend owns URI resolution; the engine keeps only indexes and caches.
func OpenContentStorage(project string, backend ContentBackend, limits core.Limits) (*Storage, error) {
	project = strings.TrimRight(strings.TrimSpace(project), "/")
	if strings.TrimSpace(project) == "" || backend == nil {
		return nil, fmt.Errorf("content storage requires a project URI and backend")
	}
	if limits.IndexBytes == 0 || limits.FileBytes == 0 || limits.Entries == 0 {
		return nil, fmt.Errorf("%w: zero storage limit", ErrLimit)
	}
	s := &Storage{
		Project: project,
		Catalog: make(map[string]int),
		limits:  limits,
		backend: backend,
	}
	entries, err := s.readContentDirectory("")
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir && strings.EqualFold(pathExt(entry.Name), ".xp3") {
			paths = append(paths, entry.Name)
		}
	}
	sort.Slice(paths, func(i, j int) bool {
		ai, an, as := archivePriority(paths[i])
		aj, bn, bs := archivePriority(paths[j])
		if ai != aj {
			return !ai
		}
		if an != bn {
			return an < bn
		}
		return as < bs
	})
	for _, archivePath := range paths {
		if err := s.Mount(archivePath, limits); err != nil && !isNotArchive(err) {
			return nil, err
		}
	}
	return s, nil
}

// OpenContentProject opens a URI-backed project and mounts its data directory
// when startup.tjs is stored there.
func OpenContentProject(project string, backend ContentBackend, limits core.Limits) (*Storage, error) {
	s, err := OpenContentStorage(project, backend, limits)
	if err != nil {
		return nil, err
	}
	if _, err := s.Resolve("startup.tjs"); err == nil {
		return s, nil
	}
	names, err := s.ReadDirNames("data")
	if err != nil {
		return nil, fmt.Errorf("project startup.tjs not found: %w", err)
	}
	for _, name := range names {
		if !strings.EqualFold(pathExt(name), ".xp3") {
			continue
		}
		if err := s.Mount("data/"+name, limits); err != nil && !isNotArchive(err) {
			return nil, err
		}
	}
	if err := s.AddSearchPath("data/"); err != nil {
		return nil, err
	}
	if _, err := s.Resolve("startup.tjs"); err != nil {
		return nil, fmt.Errorf("project startup.tjs not found in root or data")
	}
	return s, nil
}

func pathExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			break
		}
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

func isNotArchive(err error) bool {
	return errors.Is(err, ErrNotArchive) || errors.Is(err, ErrMalformed)
}

func (s *Storage) IsLocal() bool {
	return s != nil && s.backend == nil
}

func joinContentPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// readContentDirectory keeps the provider-backed directory index in the Go
// storage layer. SAF directory queries are substantially more expensive than
// local os.ReadDir calls, and startup resolves the same directories many times.
func (s *Storage) readContentDirectory(path string) ([]ContentEntry, error) {
	s.contentDirectoryMu.RLock()
	entries, ok := s.contentDirectories[path]
	s.contentDirectoryMu.RUnlock()
	if ok {
		return entries, nil
	}
	entries, err := s.backend.ReadDir(path)
	if err != nil {
		return nil, err
	}
	s.contentDirectoryMu.Lock()
	if s.contentDirectories == nil {
		s.contentDirectories = make(map[string][]ContentEntry)
	}
	if cached, exists := s.contentDirectories[path]; exists {
		entries = cached
	} else {
		if len(s.contentDirectories) >= 256 {
			s.contentDirectories = make(map[string][]ContentEntry)
		}
		s.contentDirectories[path] = entries
	}
	s.contentDirectoryMu.Unlock()
	return entries, nil
}

func (s *Storage) normalizeContent(name string, directory bool) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	outer, member, qualified := name, "", false
	if i := strings.IndexByte(name, '>'); i >= 0 {
		outer, member, qualified = name[:i], name[i+1:], true
	}
	if outer == s.Project {
		outer = ""
	} else if strings.HasPrefix(outer, s.Project+"/") {
		outer = strings.TrimPrefix(outer, s.Project+"/")
	}
	if strings.ContainsRune(outer, ':') {
		return "", fmt.Errorf("%w: content storage path contains a media prefix", core.ErrInvalidStorageName)
	}
	if qualified {
		archive, err := core.StorageName(outer)
		if err != nil {
			return "", err
		}
		if member == "" && directory {
			return archive + ">", nil
		}
		member, err = core.StorageName(member)
		if err != nil {
			return "", err
		}
		return archive + ">" + member, nil
	}
	if outer == "" || outer == "." {
		if directory {
			return "", nil
		}
		return "", fmt.Errorf("%w: empty storage name", core.ErrInvalidStorageName)
	}
	return core.StorageName(outer)
}

func (s *Storage) contentLoosePath(name string) (string, ContentEntry, bool, error) {
	var zero ContentEntry
	if strings.ContainsRune(name, '>') {
		return "", zero, false, nil
	}
	parts := strings.Split(name, "/")
	dir := ""
	for i, component := range parts {
		entries, err := s.readContentDirectory(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return "", zero, false, nil
			}
			return "", zero, false, err
		}
		key := asciiLower(component)
		var match *ContentEntry
		for index := range entries {
			if asciiLower(entries[index].Name) != key {
				continue
			}
			if match != nil {
				return "", zero, false, fmt.Errorf("ambiguous case-insensitive storage name: %s", name)
			}
			match = &entries[index]
		}
		if match == nil {
			return "", zero, false, nil
		}
		current := joinContentPath(dir, match.Name)
		if i < len(parts)-1 && !match.IsDir {
			return "", zero, false, nil
		}
		if i == len(parts)-1 {
			if match.IsDir {
				return "", zero, false, nil
			}
			return current, *match, true, nil
		}
		dir = current
	}
	return "", zero, false, nil
}

func (s *Storage) contentDirectory(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	parts := strings.Split(name, "/")
	dir := ""
	for _, component := range parts {
		entries, err := s.readContentDirectory(dir)
		if err != nil {
			return "", err
		}
		key := asciiLower(component)
		var match *ContentEntry
		for index := range entries {
			if asciiLower(entries[index].Name) == key {
				if match != nil {
					return "", fmt.Errorf("ambiguous case-insensitive storage name: %s", name)
				}
				match = &entries[index]
			}
		}
		if match == nil || !match.IsDir {
			return "", fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		dir = joinContentPath(dir, match.Name)
	}
	return dir, nil
}

func (s *Storage) contentFullPath(name string) (string, error) {
	if name == "." || strings.Trim(name, "/") == "" {
		return strings.TrimRight(s.Project, "/") + "/", nil
	}
	normalized, err := s.normalizeContent(name, false)
	if err != nil {
		return "", err
	}
	if i := strings.IndexByte(normalized, '>'); i >= 0 {
		return s.Project + "/" + normalized[:i] + ">" + normalized[i+1:], nil
	}
	return s.Project + "/" + normalized, nil
}
