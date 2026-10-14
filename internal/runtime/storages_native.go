package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// storagesNative implements the static Storages API against the session's
// existing project storage. Storage-independent path helpers remain available
// to headless sessions without mounted project storage.
func (s *Services) storagesNative(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	if operation == "@initialize" {
		return tjs.VoidValue, fmt.Errorf("Storages cannot be instantiated")
	}

	if operation == "extractStorageExt" || operation == "extractStorageName" || operation == "extractStoragePath" || operation == "chopStorageExt" {
		name, err := storagesPathArg(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		switch operation {
		case "extractStorageExt":
			return tjs.StringValue(extractStorageExt(name)), nil
		case "extractStorageName":
			return tjs.StringValue(extractStorageName(name)), nil
		case "extractStoragePath":
			return tjs.StringValue(extractStoragePath(name)), nil
		default:
			return tjs.StringValue(chopStorageExt(name)), nil
		}
	}
	if operation == "getLocalName" {
		name, err := storagesPathArg(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		storage := storagesStorage(s)
		if storage == nil {
			return tjs.VoidValue, UnsupportedDevice("storage")
		}
		local, err := storage.LocalName(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(local), nil
	}

	if operation == "fstat" {
		name, err := storagesPathArg(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		storage := storagesStorage(s)
		if storage == nil {
			return tjs.VoidValue, UnsupportedDevice("storage")
		}
		size, err := storage.StatSize(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		if size > uint64(^uint64(0)>>1) {
			return tjs.VoidValue, fmt.Errorf("Storages.fstat size exceeds TJS integer range")
		}
		info, err := tjsDictionary(vm)
		if err != nil {
			return tjs.VoidValue, err
		}
		if err := info.Set("size", tjs.IntegerValue(int64(size))); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.ObjectValue(info.Ref()), nil
	}

	if operation == "clearArchiveCache" {
		storage := storagesStorage(s)
		if storage == nil {
			return tjs.VoidValue, UnsupportedDevice("archive cache")
		}
		storage.ClearCache()
		return tjs.VoidValue, nil
	}
	if operation == "addArchive" {
		name, err := storagesPathArg(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		storage := storagesStorage(s)
		if storage == nil {
			return tjs.VoidValue, UnsupportedDevice("storage")
		}
		fullPath, err := storage.FullPath(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		if strings.ContainsRune(fullPath, '>') {
			return tjs.VoidValue, fmt.Errorf("Storages.addArchive requires an archive path, not an archive member")
		}
		const filePrefix = "file://."
		if !strings.HasPrefix(fullPath, filePrefix) {
			return tjs.VoidValue, fmt.Errorf("Storages.addArchive requires a local file archive path")
		}
		if err := storage.Mount(filepath.FromSlash(core.LocalStorageNativePath(fullPath)), s.Limits); err != nil {
			return tjs.VoidValue, err
		}
		return tjs.VoidValue, nil
	}

	if operation == "getListAt" {
		name, err := storagesPathArg(args, operation)
		if err != nil {
			return tjs.VoidValue, err
		}
		if !isVarStorage(name) || s == nil || s.varStorageVM == nil {
			return tjs.VoidValue, fmt.Errorf("Storages.getListAt supports only linked varfile storage paths")
		}
		return s.listVarStorage(vm, name)
	}
	if operation == "dirlist" || operation == "dirlistEx" {
		return s.storagesDirList(vm, operation, args)
	}
	if operation != "addAutoPath" && operation != "removeAutoPath" && operation != "getFullPath" && operation != "getPlacedPath" && operation != "isExistentStorage" {
		return tjs.VoidValue, fmt.Errorf("unknown Storages operation: %s", operation)
	}
	name, err := storagesPathArg(args, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	storage := storagesStorage(s)
	if storage == nil && !(operation == "isExistentStorage" && s != nil && s.SaveDir != "") && !(operation == "isExistentStorage" && isVarStorage(name) && s != nil && s.varStorageVM != nil) {
		return tjs.VoidValue, UnsupportedDevice("storage")
	}

	switch operation {
	case "addAutoPath":
		return tjs.VoidValue, storage.AddSearchPath(name)
	case "removeAutoPath":
		return tjs.VoidValue, storage.RemoveSearchPath(name)
	case "getFullPath":
		fullPath, err := storage.FullPath(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(fullPath), nil
	case "getPlacedPath":
		placedPath, err := storage.PlacedPath(name)
		if errors.Is(err, assets.ErrNotFound) {
			return tjs.StringValue(""), nil
		}
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(placedPath), nil
	case "isExistentStorage":
		if isVarStorage(name) {
			if s == nil || s.varStorageVM == nil {
				return tjs.IntegerValue(0), nil
			}
			if _, err := s.readVarStorage(vm, name); err != nil {
				return tjs.IntegerValue(0), nil
			}
			return tjs.IntegerValue(1), nil
		}
		if name == "" {
			return tjs.IntegerValue(0), nil
		}
		if s != nil && s.SaveDir != "" {
			save := SaveStorage{Root: s.SaveDir, Project: serviceProjectPath(s)}
			exists, err := save.Exists(name)
			if err == nil && exists {
				return tjs.IntegerValue(1), nil
			}
		}
		if storage == nil {
			return tjs.IntegerValue(0), nil
		}
		if _, err := storage.Resolve(name); err != nil {
			return tjs.IntegerValue(0), nil
		}
		return tjs.IntegerValue(1), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Storages operation: %s", operation)
	}
}

func storagesStorage(s *Services) *assets.Storage {
	if s == nil {
		return nil
	}
	return s.Storage
}

// POSIX file-type bits carried by the Storages.dirlistEx "attrib" field. The
// plugin manual tells scripts to determine directory status from attrib, so the
// value keeps the S_IFMT type bits alongside the permission bits.
const (
	storageAttribDirectory = 0x4000
	storageAttribRegular   = 0x8000
)

// storagesDirList implements the read-only directory enumeration of fstat.dll.
// Both methods require a directory name ending in a separator, resolve it
// inside the local file-media storage space, and return the direct children of
// that directory: Storages.dirlist yields entry names and Storages.dirlistEx
// yields dictionaries with name, size, attrib, mtime, atime, and ctime. Like
// the reference plugin, the listing covers the project-local directory and does
// not enumerate XP3 archive contents.
func (s *Services) storagesDirList(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("Storages.%s requires argument 0", operation)
	}
	name := args[0].Text()
	if name == "" || (name[len(name)-1] != '/' && name[len(name)-1] != '\\') {
		return tjs.VoidValue, fmt.Errorf("Storages.%s: '/' must be specified at the end of given directory name.", operation)
	}
	storage := storagesStorage(s)
	if storage == nil {
		return tjs.VoidValue, UnsupportedDevice("storage")
	}
	if operation == "dirlist" {
		names, err := storage.ReadDirNames(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		values := make([]tjs.Value, len(names))
		for i, item := range names {
			values[i] = tjs.StringValue(item)
		}
		return vm.NewArray(values)
	}
	entries, err := storage.ReadDirEntries(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	values := make([]tjs.Value, 0, len(entries))
	for _, entry := range entries {
		info, err := tjsDictionary(vm)
		if err != nil {
			return tjs.VoidValue, err
		}
		fields := []struct {
			name  string
			value tjs.Value
		}{
			{"name", tjs.StringValue(entry.Name)},
			{"size", tjs.IntegerValue(entry.Size)},
			{"attrib", tjs.IntegerValue(storageDirAttrib(entry))},
			{"mtime", tjs.IntegerValue(entry.ModTime)},
			{"atime", tjs.IntegerValue(entry.Atime)},
			{"ctime", tjs.IntegerValue(entry.Ctime)},
		}
		for _, field := range fields {
			if err := info.Set(field.name, field.value); err != nil {
				return tjs.VoidValue, fmt.Errorf("Storages.%s %s: %w", operation, field.name, err)
			}
		}
		values = append(values, tjs.ObjectValue(info.Ref()))
	}
	return vm.NewArray(values)
}

// storageDirAttrib maps a directory entry to the Storages.dirlistEx attrib
// value: POSIX permission bits plus the file-type bit for directories and
// regular files.
func storageDirAttrib(entry assets.DirEntry) int64 {
	attrib := int64(entry.Mode.Perm())
	switch {
	case entry.IsDir:
		attrib |= storageAttribDirectory
	case entry.Mode.IsRegular():
		attrib |= storageAttribRegular
	}
	return attrib
}

func storagesPathArg(args []tjs.Value, operation string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("Storages.%s requires argument 0", operation)
	}
	return args[0].Text(), nil
}

func extractStorageExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '/', '\\', '>':
			return ""
		case '.':
			return name[i:]
		}
	}
	return ""
}

func extractStorageName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '/', '\\', '>':
			return name[i+1:]
		}
	}
	return name
}

func extractStoragePath(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '/', '\\', '>':
			return name[:i+1]
		}
	}
	return ""
}

func chopStorageExt(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		switch name[i] {
		case '/', '\\', '>':
			return name
		case '.':
			return name[:i]
		}
	}
	return name
}
