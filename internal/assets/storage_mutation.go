package assets

import (
	"fmt"
	"os"
	"strings"
)

// ExistsNoSearch reports whether name denotes an existing loose file or archive
// member without consulting registered search paths. It mirrors
// TVPIsExistentStorageNoSearchNoNormalize in the reference storage backend:
// only the storage itself is inspected, so a name that is found exclusively
// through an auto-path is reported as absent.
func (s *Storage) ExistsNoSearch(name string) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("nil storage")
	}
	if name == "" {
		return false, nil
	}
	normalized, err := s.normalize(name, false)
	if err != nil {
		return false, err
	}
	return s.contains(normalized)
}

// LooseFile returns the canonical operating-system path of an existing loose
// file contained by the project. Archive-qualified names yield "" without an
// error because archive members have no local file. Missing names also yield
// "" so callers can separate "absent" from "invalid".
func (s *Storage) LooseFile(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil storage")
	}
	normalized, err := s.normalize(name, false)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(normalized, '>') {
		return "", nil
	}
	return s.loosePath(normalized)
}

// DirectoryPath resolves an existing project-local directory without search
// paths, applying the same case-insensitive component lookup and symlink
// containment checks used by directory enumeration.
func (s *Storage) DirectoryPath(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("nil storage")
	}
	normalized, err := s.normalize(name, true)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(normalized, '>') {
		return "", fmt.Errorf("archive directories are not locally accessible: %s", normalized)
	}
	return s.resolveDirectory(normalized)
}

// FileTimes returns one file's modification, access, and creation/change times
// in Unix seconds. fileTimes supplies the platform-specific access and
// creation values and falls back to the modification time where the host
// filesystem exposes nothing better.
func FileTimes(info os.FileInfo) (mtime, atime, ctime int64) {
	if info == nil {
		return 0, 0, 0
	}
	mtime = info.ModTime().Unix()
	atime, ctime = fileTimes(info, mtime)
	return mtime, atime, ctime
}
