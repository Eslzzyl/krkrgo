package runtime

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

var (
	ErrInvalidSaveName = errors.New("invalid save name")
	ErrSaveNotFound    = errors.New("save not found")
	ErrSavePath        = errors.New("invalid save path")
)

// SaveStorage is a writable overlay rooted in the policy-approved save
// directory. Base, when supplied, remains read-only and is consulted only
// after the overlay, allowing scripts to save over packaged resources without
// modifying the installation.
type SaveStorage struct {
	Root    string
	Project string
	Base    *assets.Storage
}

// NewSaveStorage creates the save directory if necessary. core.SaveDirectory
// performs project containment and symlink-ancestor checks before this method
// creates anything.
func NewSaveStorage(project string, override *string) (*SaveStorage, error) {
	root, err := core.SaveDirectory(project, override)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create save directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve save directory: %w", err)
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%w: save root is not a directory", ErrSavePath)
	}
	projectRoot, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	projectRoot, err = filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return nil, err
	}
	return &SaveStorage{Root: root, Project: projectRoot}, nil
}

func NewSaveOverlay(project string, override *string) (*SaveStorage, error) {
	return NewSaveStorage(project, override)
}
func NewSaveStorageWithBase(project string, override *string, base *assets.Storage) (*SaveStorage, error) {
	s, err := NewSaveStorage(project, override)
	if err != nil {
		return nil, err
	}
	s.Base = base
	return s, nil
}
func (s *SaveStorage) SetBase(base *assets.Storage) {
	if s != nil {
		s.Base = base
	}
}
func (s *SaveStorage) Directory() string {
	if s == nil {
		return ""
	}
	return s.Root
}
func serviceProjectPath(s *Services) string {
	if s == nil || s.Storage == nil {
		return ""
	}
	return s.Storage.Project
}

func saveStorageName(project, root, name string) (string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	candidate := core.LocalStorageNativePath(name)
	candidatePath := filepath.FromSlash(candidate)
	if filepath.IsAbs(candidatePath) {
		for _, base := range []string{root, project} {
			if base == "" {
				continue
			}
			relative, matched, err := core.LocalStorageRelativePath(candidate, base)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidSaveName, err)
			}
			if !matched {
				continue
			}
			n, err := core.StorageName(relative)
			if err != nil {
				return "", fmt.Errorf("%w: %w", ErrInvalidSaveName, err)
			}
			return n, nil
		}
		return "", fmt.Errorf("%w: absolute storage is outside project and save directories", ErrInvalidSaveName)
	}
	n, err := core.StorageName(candidate)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidSaveName, err)
	}
	return n, nil
}

func saveStoragePath(project, root, name string) (string, string, error) {
	if root == "" {
		return "", "", fmt.Errorf("%w: nil save storage", ErrSavePath)
	}
	n, err := saveStorageName(project, root, name)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(root, filepath.FromSlash(n))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidSaveName, name)
	}
	return n, path, nil
}

func (s *SaveStorage) path(name string) (string, string, error) {
	if s == nil || s.Root == "" {
		return "", "", fmt.Errorf("%w: nil save storage", ErrSavePath)
	}
	return saveStoragePath(s.Project, s.Root, name)
}

// ensureParent creates only ordinary directories below the canonical root and
// rejects symlinked components. This keeps all writes inside the save overlay.
func (s *SaveStorage) ensureParent(path string) error {
	parent := filepath.Dir(path)
	rel, err := filepath.Rel(s.Root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrSavePath
	}
	cur := s.Root
	if rel != "." {
		for _, component := range strings.Split(rel, string(filepath.Separator)) {
			if component == "" || component == "." {
				continue
			}
			cur = filepath.Join(cur, component)
			info, statErr := os.Lstat(cur)
			if statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
					return fmt.Errorf("%w: parent component %q", ErrSavePath, component)
				}
				continue
			}
			if !os.IsNotExist(statErr) {
				return statErr
			}
			if err := os.Mkdir(cur, 0o755); err != nil && !os.IsExist(err) {
				return err
			}
			info, err = os.Lstat(cur)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("%w: parent component %q", ErrSavePath, component)
			}
		}
	}
	return nil
}
func (s *SaveStorage) checkParent(path string) error {
	parent := filepath.Dir(path)
	rel, err := filepath.Rel(s.Root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrSavePath
	}
	cur := s.Root
	if rel == "." {
		return nil
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		cur = filepath.Join(cur, component)
		info, statErr := os.Lstat(cur)
		if os.IsNotExist(statErr) {
			return nil
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("%w: parent component %q", ErrSavePath, component)
		}
	}
	return nil
}

func (s *SaveStorage) target(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: symlink target", ErrSavePath)
	}
	return info, nil
}

func (s *SaveStorage) Read(name string) ([]byte, error) {
	n, path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	if err := s.checkParent(path); err != nil {
		return nil, err
	}
	if info, statErr := s.target(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: not a regular file", ErrSavePath)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return data, nil
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if s.Base != nil {
		data, baseErr := s.Base.Read(n)
		if baseErr == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrSaveNotFound, n)
}

func (s *SaveStorage) ReadFile(name string) ([]byte, error) { return s.Read(name) }

// Write atomically replaces one overlay file. The temporary file is created
// in the destination directory, then synced and renamed, so a crash exposes
// either the previous complete file or the new complete file.
func (s *SaveStorage) Write(name string, data []byte) error {
	return s.writeAt(name, data, 0)
}

func (s *SaveStorage) writeAt(name string, data []byte, offset int64) error {
	return s.writeAtMode(name, data, offset, 0o666)
}

func (s *SaveStorage) writeAtMode(name string, data []byte, offset int64, mode os.FileMode) error {
	if offset < 0 {
		return fmt.Errorf("negative save offset")
	}
	_, path, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return err
	}
	if err := s.ensureParent(path); err != nil {
		return err
	}
	if info, statErr := s.target(path); statErr == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%w: destination is not a regular file", ErrSavePath)
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if offset > 0 {
		old, _ := os.ReadFile(path)
		if int64(len(old)) < offset {
			old = append(old, make([]byte, int(offset)-len(old))...)
		}
		old = append(old[:int(offset)], data...)
		data = old
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".krkrgo-save-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
func (s *SaveStorage) WriteFile(name string, data []byte) error { return s.Write(name, data) }
func (s *SaveStorage) Save(name string, data []byte) error      { return s.Write(name, data) }

func (s *SaveStorage) Delete(name string) error {
	_, path, err := s.path(name)
	if err != nil {
		return err
	}
	if err := s.checkParent(path); err != nil {
		return err
	}
	if info, statErr := s.target(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil
		}
		return statErr
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: not a regular file", ErrSavePath)
	}
	return os.Remove(path)
}
func (s *SaveStorage) Exists(name string) (bool, error) {
	_, path, err := s.path(name)
	if err != nil {
		return false, err
	}
	if err := s.checkParent(path); err != nil {
		return false, err
	}
	if info, statErr := s.target(path); statErr == nil {
		return info.Mode().IsRegular(), nil
	} else if !os.IsNotExist(statErr) {
		return false, statErr
	}
	if s.Base != nil {
		_, baseErr := s.Base.Read(name)
		return baseErr == nil, nil
	}
	return false, nil
}

// List enumerates overlay names. An optional prefix is normalized as a
// directory-like storage name; returned names use slash separators and are
// sorted for deterministic saves/replays.
func (s *SaveStorage) List(prefix ...string) ([]string, error) {
	root := s.Root
	if len(prefix) > 0 && prefix[0] != "" {
		n, _, err := s.path(prefix[0])
		if err != nil {
			return nil, err
		}
		root = filepath.Join(s.Root, filepath.FromSlash(n))
	}
	if info, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	} else if !info.IsDir() {
		return nil, ErrSavePath
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
