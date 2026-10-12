package assets

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/core"
)

// ErrUnsupportedStorageMedia identifies a storage name whose media backend is
// not available to the project-backed Go storage implementation.
var ErrUnsupportedStorageMedia = errors.New("unsupported storage media")

// Storage resolves loose files and mounted XP3 archives. Later archives and
// later search paths override earlier ones; loose files override archives.
type Storage struct {
	Project     string
	Archives    []*Archive
	Catalog     map[string]int
	limits      core.Limits
	searchPaths []string
	imageCache  imageDecodeCache

	directoryMu  sync.Mutex
	directories  map[string]directoryIndex
	directoryAge uint64
	cacheProject string
}

const maxStorageDirectoryIndexes = 128

type directoryIndex struct {
	info    os.FileInfo
	entries map[string]directoryEntry
	age     uint64
}

type directoryEntry struct {
	name      string
	collision bool
}

func sameDirectoryMetadata(old, current os.FileInfo) bool {
	return os.SameFile(old, current) && old.IsDir() == current.IsDir() &&
		old.Mode() == current.Mode() && old.Size() == current.Size() &&
		old.ModTime().Equal(current.ModTime())
}

func (s *Storage) directoryEntries(path string, info os.FileInfo) (map[string]directoryEntry, error) {
	s.directoryMu.Lock()
	if s.cacheProject != s.Project {
		s.directories = nil
		s.cacheProject = s.Project
	}
	if index, ok := s.directories[path]; ok && sameDirectoryMetadata(index.info, info) {
		s.directoryAge++
		index.age = s.directoryAge
		s.directories[path] = index
		s.directoryMu.Unlock()
		return index.entries, nil
	}
	s.directoryMu.Unlock()

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	indexed := make(map[string]directoryEntry, len(entries))
	for _, entry := range entries {
		key := asciiLower(entry.Name())
		if previous, ok := indexed[key]; ok {
			previous.collision = true
			indexed[key] = previous
		} else {
			indexed[key] = directoryEntry{name: entry.Name()}
		}
	}

	s.directoryMu.Lock()
	if s.directories == nil {
		s.directories = make(map[string]directoryIndex)
	}
	s.directoryAge++
	s.directories[path] = directoryIndex{info: info, entries: indexed, age: s.directoryAge}
	if len(s.directories) > maxStorageDirectoryIndexes {
		oldestPath := ""
		oldestAge := ^uint64(0)
		for candidate, cached := range s.directories {
			if cached.age < oldestAge {
				oldestPath, oldestAge = candidate, cached.age
			}
		}
		delete(s.directories, oldestPath)
	}
	s.directoryMu.Unlock()
	return indexed, nil
}

func (s *Storage) clearDirectoryIndexes() {
	s.directoryMu.Lock()
	s.directories = nil
	s.cacheProject = s.Project
	s.directoryMu.Unlock()
}

func OpenStorage(project string, limits core.Limits) (*Storage, error) {
	project, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(project)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("project is not a directory: %s", project)
	}
	if limits.IndexBytes == 0 || limits.FileBytes == 0 || limits.Entries == 0 {
		return nil, fmt.Errorf("%w: zero storage limit", ErrLimit)
	}
	entries, err := os.ReadDir(project)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0)
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".xp3") {
			paths = append(paths, filepath.Join(project, e.Name()))
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
	s := &Storage{Project: project, Catalog: make(map[string]int), limits: limits}
	for _, path := range paths {
		if err := s.Mount(path, limits); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func NewStorage(project string, limits core.Limits) (*Storage, error) {
	return OpenStorage(project, limits)
}

func (s *Storage) Mount(path string, limits core.Limits) error {
	a, err := OpenArchive(path, limits)
	if err != nil {
		return err
	}
	index := len(s.Archives)
	for name := range a.Entries {
		s.Catalog[name] = index
	}
	s.Archives = append(s.Archives, a)
	s.clearDirectoryIndexes()
	return nil
}

func (s *Storage) AddSearchPath(path string) error {
	if s == nil {
		return fmt.Errorf("nil storage")
	}
	if !hasStoragePathDelimiter(path) {
		return fmt.Errorf("storage search path must end with /, \\, or >")
	}
	n, err := s.normalize(path, true)
	if err != nil {
		return err
	}
	for i := len(s.searchPaths) - 1; i >= 0; i-- {
		if s.searchPaths[i] == n {
			s.searchPaths = append(s.searchPaths[:i], s.searchPaths[i+1:]...)
			break
		}
	}
	s.searchPaths = append(s.searchPaths, n)
	s.clearDirectoryIndexes()
	return nil
}

func (s *Storage) RemoveSearchPath(path string) error {
	if s == nil {
		return fmt.Errorf("nil storage")
	}
	if !hasStoragePathDelimiter(path) {
		return fmt.Errorf("storage search path must end with /, \\, or >")
	}
	n, err := s.normalize(path, true)
	if err != nil {
		return err
	}
	for i := len(s.searchPaths) - 1; i >= 0; i-- {
		if s.searchPaths[i] == n {
			s.searchPaths = append(s.searchPaths[:i], s.searchPaths[i+1:]...)
			break
		}
	}
	s.clearDirectoryIndexes()
	return nil
}

func hasStoragePathDelimiter(path string) bool {
	if path == "" {
		return false
	}
	switch path[len(path)-1] {
	case '/', '\\', '>':
		return true
	default:
		return false
	}
}

// NormalizeStorageName applies this storage's project-relative path rules,
// including normalization of archive members separated by '>'.
func (s *Storage) NormalizeStorageName(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil storage")
	}
	return s.normalize(name, false)
}

// ClearCache discards cached XP3 segment and decoded-image data.
func (s *Storage) ClearCache() {
	if s == nil {
		return
	}
	for _, archive := range s.Archives {
		if archive != nil {
			archive.ClearCache()
		}
	}
	s.imageCache.clear()
	s.clearDirectoryIndexes()
}

// FullPath returns a normalized file-media storage name. Relative names are
// rooted at the canonical project directory; it does not access the file.
func (s *Storage) FullPath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if s == nil {
		return "", fmt.Errorf("nil storage")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("NUL in storage name")
	}
	name = strings.ReplaceAll(name, `\`, "/")
	outer, member, qualified := name, "", false
	if i := strings.IndexByte(name, '>'); i >= 0 {
		outer, member, qualified = name[:i], name[i+1:], true
	}
	if outer == "" {
		return "", fmt.Errorf("archive storage requires an archive name")
	}
	outer, err := s.storagePath(outer)
	if err != nil {
		return "", err
	}
	if drive, ok := localDriveName(outer); ok {
		// "file://./c:/games" keeps the volume behind the leading slash.
		outer = drive
	}
	if strings.ContainsRune(outer, ':') && !driveQualifiedPath(outer) {
		return "", fmt.Errorf("%w: file path contains a colon", ErrUnsupportedStorageMedia)
	}
	switch {
	case driveQualifiedPath(outer):
		outer = "/" + filepath.ToSlash(outer)
	case !strings.HasPrefix(outer, "/"):
		outer = filepath.ToSlash(s.Project) + "/" + outer
	}
	outer = asciiLower(outer)
	outer, err = normalizeFullComponent(filepath.ToSlash(outer), true)
	if err != nil {
		return "", err
	}
	result := "file://." + outer
	if qualified {
		if strings.ContainsRune(member, '>') {
			return "", fmt.Errorf("nested archive storage is not supported")
		}
		if strings.ContainsRune(member, ':') {
			return "", fmt.Errorf("%w: archive member path", ErrUnsupportedStorageMedia)
		}
		member, err = normalizeFullComponent(member, false)
		if err != nil {
			return "", err
		}
		member = asciiLower(member)
		result += ">" + member
	}
	return result, nil
}

// LocalName returns the operating-system path for a file-media storage name.
// Archive members cannot be passed directly to the local filesystem.
func (s *Storage) LocalName(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil storage")
	}
	if strings.ContainsRune(name, '>') {
		return "", fmt.Errorf("archive members are not locally accessible")
	}
	name = strings.ReplaceAll(name, `\`, "/")
	local, err := s.storagePath(name)
	if err != nil {
		return "", err
	}
	local = filepath.FromSlash(core.LocalStorageNativePath(local))
	if !filepath.IsAbs(local) {
		local = filepath.Join(s.Project, local)
	}
	return filepath.Clean(local), nil
}

// storagePath accepts unqualified project names and the local file media
// spelling. Other media are unsupported by the project-only storage backend.
func (s *Storage) storagePath(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("%w: NUL in storage name", core.ErrInvalidStorageName)
	}
	if colon := storageMediaSeparator(name); colon >= 0 {
		media := asciiLower(name[:colon])
		if media != "file" {
			return "", fmt.Errorf("%w: %s", ErrUnsupportedStorageMedia, media)
		}
		return s.localFileMediaPath(name[colon+1:])
	}
	if strings.ContainsRune(name, ':') && !driveQualifiedPath(name) {
		return "", fmt.Errorf("%w: drive-qualified or malformed storage name", ErrUnsupportedStorageMedia)
	}
	return name, nil
}

// windowsVolumePaths reports whether absolute local paths start with a volume
// name, as they do on Windows. File-media storage names keep a leading slash,
// so a drive path appears as "/c:/games/data.xp3" inside those names.
var windowsVolumePaths = filepath.Separator == '\\'

// driveQualifiedPath reports the native Windows spelling of an absolute drive
// path, such as "c:/games" or `c:\games`.
func driveQualifiedPath(name string) bool {
	if !windowsVolumePaths || len(name) < 3 || name[1] != ':' {
		return false
	}
	return isASCIILetter(name[0]) && (name[2] == '/' || name[2] == '\\')
}

// localDriveName reports the file-media spelling of an absolute drive path,
// "/c:/games", and returns the native slash spelling "c:/games".
func localDriveName(name string) (string, bool) {
	if !windowsVolumePaths || len(name) < 4 || name[0] != '/' || name[2] != ':' || name[3] != '/' {
		return "", false
	}
	if !isASCIILetter(name[1]) {
		return "", false
	}
	return name[1:], true
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func storageMediaSeparator(name string) int {
	if driveQualifiedPath(name) {
		return -1
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			continue
		}
		if c == ':' && i != 0 {
			return i
		}
		return -1
	}
	return -1
}

func (s *Storage) localFileMediaPath(path string) (string, error) {
	if path == "" {
		return "/", nil
	}
	if strings.HasPrefix(path, "///") {
		return path[2:], nil
	}
	if strings.HasPrefix(path, "//") {
		authority := path[2:]
		slash := strings.IndexByte(authority, '/')
		if slash < 0 {
			return "", fmt.Errorf("%w: malformed file storage URI", core.ErrInvalidStorageName)
		}
		domain := asciiLower(authority[:slash])
		if domain != "." {
			return "", fmt.Errorf("unsupported file storage domain: %s", domain)
		}
		return authority[slash:], nil
	}
	if drive, ok := localDriveName(path); ok {
		return drive, nil
	}
	if strings.HasPrefix(path, "/") {
		return path, nil
	}
	return filepath.ToSlash(s.Project) + "/" + path, nil
}

func (s *Storage) normalize(name string, directory bool) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	outer, member, qualified := name, "", false
	if i := strings.IndexByte(name, '>'); i >= 0 {
		outer, member, qualified = name[:i], name[i+1:], true
	}
	var err error
	outer, err = s.storagePath(outer)
	if err != nil {
		return "", err
	}
	// The archive file path is resolved before the member is appended: a
	// Windows member separator is not a legal filename character, so a combined
	// path could not be inspected on the filesystem.
	outer, err = s.projectRelativePath(outer)
	if err != nil {
		return "", err
	}
	name = outer
	if qualified {
		name += ">" + member
	}
	if i := strings.IndexByte(name, '>'); i >= 0 {
		archive, member := name[:i], name[i+1:]
		if strings.Contains(member, ">") {
			return "", fmt.Errorf("nested archive storage is not supported")
		}
		a, err := core.StorageName(archive)
		if err != nil {
			return "", err
		}
		if directory && member == "" {
			return a + ">", nil
		}
		m, err := core.StorageName(member)
		if err != nil {
			return "", err
		}
		return a + ">" + m, nil
	}
	if directory && (name == "" || name == "." || name == "./") {
		return "", nil
	}
	return core.StorageName(name)
}

// projectRelativePath rewrites an absolute local path inside the project as a
// project-relative name; other names pass through unchanged.
func (s *Storage) projectRelativePath(name string) (string, error) {
	if relative, matched, err := core.LocalStorageRelativePath(name, s.Project); err != nil {
		return "", err
	} else if matched {
		return relative, nil
	}
	// FullPath lowercases file-media URIs for storage-name compatibility. On
	// case-sensitive volumes that can also lowercase the absolute project
	// prefix, so accept that spelling only when it still names this project.
	// Re-resolve against the canonical project root to reject symlink escapes.
	relative, matched := projectRelativePathFolded(name, s.Project)
	if !matched {
		return name, nil
	}
	candidate := filepath.Join(s.Project, filepath.FromSlash(relative))
	resolved, inProject, err := core.LocalStorageRelativePath(candidate, s.Project)
	if err != nil {
		return "", err
	}
	if !inProject {
		return "", fmt.Errorf("%w: absolute storage name %q", core.ErrInvalidStorageName, name)
	}
	return resolved, nil
}

// projectRelativePathFolded handles absolute project paths returned by
// FullPath after its storage-name case normalization. It never grants access
// outside the project; loosePath still enforces symlink containment.
func projectRelativePathFolded(name, project string) (string, bool) {
	path := filepath.Clean(filepath.FromSlash(strings.ReplaceAll(name, `\`, "/")))
	root := filepath.Clean(project)
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return "", false
	}
	pathParts := strings.Split(filepath.ToSlash(path), "/")
	rootParts := strings.Split(filepath.ToSlash(root), "/")
	if len(pathParts) < len(rootParts) {
		return "", false
	}
	for i := range rootParts {
		if !strings.EqualFold(pathParts[i], rootParts[i]) {
			return "", false
		}
	}
	return filepath.ToSlash(filepath.Join(pathParts[len(rootParts):]...)), true
}

func (s *Storage) archiveLocation(name string) (int, string, bool) {
	if i := strings.IndexByte(name, '>'); i >= 0 {
		archive, member := name[:i], name[i+1:]
		archive = asciiLower(archive)
		for idx, a := range s.Archives {
			base := filepath.Base(a.Path)
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			relative, err := filepath.Rel(s.Project, a.Path)
			if err != nil {
				continue
			}
			relative = filepath.ToSlash(relative)
			if archive == asciiLower(base) || archive == asciiLower(stem) || archive == asciiLower(relative) {
				if _, ok := a.Entries[member]; ok {
					return idx, member, true
				}
			}
		}
		return 0, "", false
	}
	idx, ok := s.Catalog[name]
	if !ok {
		return 0, "", false
	}
	return idx, name, true
}

func (s *Storage) Resolve(name string) (string, error) {
	n, err := s.normalize(name, false)
	if err != nil {
		return "", err
	}
	if ok, err := s.contains(n); err != nil {
		return "", err
	} else if ok {
		return n, nil
	}
	if !strings.ContainsRune(n, '>') {
		for i := len(s.searchPaths) - 1; i >= 0; i-- {
			path := s.searchPaths[i]
			sep := "/"
			if path == "" || strings.HasSuffix(path, ">") {
				sep = ""
			}
			candidate := path + sep + n
			if ok, err := s.contains(candidate); err != nil {
				return "", err
			} else if ok {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, n)
}

func (s *Storage) contains(name string) (bool, error) {
	path, err := s.loosePath(name)
	if err != nil {
		return false, err
	}
	if path != "" {
		return true, nil
	}
	_, _, ok := s.archiveLocation(name)
	return ok, nil
}

// exactLoosePath accepts only canonical storage names. Failed lookups,
// non-regular files, and symlinks outside the project fall through to the
// existing case-insensitive resolver so its behavior and error reporting stay
// authoritative.
func (s *Storage) exactLoosePath(name string) (string, bool) {
	normalized, err := core.StorageName(name)
	if err != nil || normalized != name {
		return "", false
	}
	candidate := filepath.Join(s.Project, filepath.FromSlash(name))
	if _, err := os.Lstat(candidate); os.IsNotExist(err) {
		return "", false
	} else if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil || !within(s.Project, canonical) {
		return "", false
	}
	st, err := os.Stat(canonical)
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return canonical, true
}

// loosePath performs a case-insensitive component lookup while rechecking
// symlink targets against the canonical project root on every call. Normalized
// project-relative names first try an exact path to avoid scanning each
// directory when the on-disk spelling already matches.
func (s *Storage) loosePath(name string) (string, error) {
	if strings.ContainsRune(name, '>') {
		return "", nil
	}
	if path, ok := s.exactLoosePath(name); ok {
		return path, nil
	}
	parts := strings.Split(name, "/")
	path := s.Project
	for _, component := range parts {
		if component == "" {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		if !st.IsDir() {
			return "", nil
		}
		entries, err := s.directoryEntries(path, st)
		if err != nil {
			return "", err
		}
		entry, ok := entries[asciiLower(component)]
		if ok && entry.collision {
			return "", fmt.Errorf("ambiguous case-insensitive storage name: %s", name)
		}
		match := ""
		if ok {
			match = entry.name
		}
		if match == "" {
			return "", nil
		}
		path = filepath.Join(path, match)
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "", err
		}
		if !within(s.Project, canonical) {
			return "", fmt.Errorf("storage symlink escapes game directory: %s", name)
		}
		path = canonical
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", nil
	}
	return path, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (s *Storage) Read(name string) ([]byte, error) {
	// The common project-root path avoids resolving a loose file once in
	// Resolve and then repeating the same filesystem checks to open/read it.
	// Search paths and archive-qualified names retain the general resolver,
	// whose ordering covers those less common cases.
	if s != nil && len(s.searchPaths) == 0 && !strings.ContainsRune(name, '>') {
		normalized, err := s.normalize(name, false)
		if err != nil {
			return nil, err
		}
		if path, err := s.loosePath(normalized); err != nil {
			return nil, err
		} else if path != "" {
			return s.readLoosePath(path)
		}
		if index, ok := s.Catalog[normalized]; ok && index >= 0 && index < len(s.Archives) {
			return s.Archives[index].Read(normalized)
		}
		return nil, fmt.Errorf("%w: %s", ErrNotFound, normalized)
	}
	resolved, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}
	if path, err := s.loosePath(resolved); err != nil {
		return nil, err
	} else if path != "" {
		return s.readLoosePath(path)
	}
	idx, member, ok := s.archiveLocation(resolved)
	if !ok {
		return nil, fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	return s.Archives[idx].Read(member)
}

func (s *Storage) readLoosePath(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if uint64(st.Size()) > s.limits.FileBytes {
		return nil, fmt.Errorf("%w: loose storage exceeds limit", ErrLimit)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(s.limits.FileBytes)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(data)) > s.limits.FileBytes {
		return nil, fmt.Errorf("%w: loose storage exceeds limit", ErrLimit)
	}
	return data, nil
}

// PlacedPath returns the normalized file-media name of a resolved storage
// entry, or an error when the entry is missing.
func (s *Storage) PlacedPath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	resolved, err := s.Resolve(name)
	if err != nil {
		return "", err
	}
	if p, err := s.loosePath(resolved); err != nil {
		return "", err
	} else if p != "" {
		return s.FullPath(resolved)
	}
	idx, member, ok := s.archiveLocation(resolved)
	if !ok {
		return "", fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	archivePath, err := filepath.Abs(s.Archives[idx].Path)
	if err != nil {
		return "", err
	}
	return s.FullPath(archivePath + ">" + member)
}

// StatSize returns the logical size of a resolved loose file or archive entry.
func (s *Storage) StatSize(name string) (uint64, error) {
	if s == nil {
		return 0, fmt.Errorf("nil storage")
	}
	if strings.ContainsRune(name, ':') {
		outer, member, qualified := name, "", false
		if i := strings.IndexByte(name, '>'); i >= 0 {
			outer, member, qualified = name[:i], name[i+1:], true
		}
		placed, err := s.storagePath(outer)
		if err != nil {
			return 0, err
		}
		placed = core.LocalStorageNativePath(placed)
		if filepath.IsAbs(placed) {
			relative, ok := storageRelativeCaseInsensitive(placed, s.Project)
			if !ok {
				return 0, fmt.Errorf("%w: placed path is outside project", core.ErrInvalidStorageName)
			}
			name = relative
			if qualified {
				name += ">" + member
			}
		}
	}
	resolved, err := s.Resolve(name)
	if err != nil {
		return 0, err
	}
	if path, err := s.loosePath(resolved); err != nil {
		return 0, err
	} else if path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return uint64(info.Size()), nil
	}
	index, member, ok := s.archiveLocation(resolved)
	if !ok {
		return 0, fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	entry, ok := s.Archives[index].Entries[member]
	if !ok {
		return 0, fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	return entry.Size, nil
}

func storageRelativeCaseInsensitive(path, root string) (string, bool) {
	path = filepath.ToSlash(filepath.Clean(path))
	root = filepath.ToSlash(filepath.Clean(root))
	foldedPath, foldedRoot := asciiLower(path), asciiLower(root)
	if foldedPath == foldedRoot {
		return "", true
	}
	prefix := strings.TrimRight(foldedRoot, "/") + "/"
	if !strings.HasPrefix(foldedPath, prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

func (s *Storage) Entry(name string) (string, *Entry, error) {
	resolved, err := s.Resolve(name)
	if err != nil {
		return "", nil, err
	}
	if p, err := s.loosePath(resolved); err != nil {
		return "", nil, err
	} else if p != "" {
		return "", nil, fmt.Errorf("storage is a loose file, not an archive entry")
	}
	idx, member, ok := s.archiveLocation(resolved)
	if !ok {
		return "", nil, fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	e := s.Archives[idx].Entries[member]
	return s.Archives[idx].Path, &e, nil
}

func (s *Storage) Verify(name string) error {
	resolved, err := s.Resolve(name)
	if err != nil {
		return err
	}
	if p, err := s.loosePath(resolved); err != nil {
		return err
	} else if p != "" {
		return fmt.Errorf("loose file has no XP3 checksum: %s", resolved)
	}
	idx, member, ok := s.archiveLocation(resolved)
	if !ok {
		return fmt.Errorf("%w: archive entry disappeared", ErrNotFound)
	}
	return s.Archives[idx].Verify(member)
}

// Extract writes one explicitly named resource without overwriting an existing
// file, and refuses destinations inside (or through symlinks into) Project.
func (s *Storage) Extract(name, destination string) error {
	data, err := s.Read(name)
	if err != nil {
		return err
	}
	parent := filepath.Dir(destination)
	if parent == "" {
		parent = "."
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if within(s.Project, parent) {
		return fmt.Errorf("extraction destination must be outside the game installation")
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func normalizeFullComponent(path string, absolute bool) (string, error) {
	parts := make([]string, 0)
	directory := strings.HasSuffix(path, "/")
	components := strings.Split(path, "/")
	for _, part := range components {
		if part == "" {
			continue
		}
		if strings.Trim(part, ".") == "" {
			parents := len(part) - 1
			if parents > len(parts) {
				return "", fmt.Errorf("storage path escapes its root")
			}
			parts = parts[:len(parts)-parents]
			directory = true
		} else {
			parts = append(parts, part)
			directory = false
		}
	}
	result := strings.Join(parts, "/")
	if absolute {
		result = "/" + result
	}
	if (directory || strings.HasSuffix(path, "/")) && result != "" && !strings.HasSuffix(result, "/") {
		result += "/"
	}
	return result, nil
}

func archivePriority(path string) (bool, int, string) {
	name := asciiLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if !strings.HasPrefix(name, "patch") {
		return false, 0, name
	}
	n := strings.TrimPrefix(name, "patch")
	if n == "" {
		return true, 0, name
	}
	v, err := strconv.Atoi(n)
	if err != nil {
		return false, 0, name
	}
	return true, v, name
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
