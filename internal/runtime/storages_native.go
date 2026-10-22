package runtime

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// storagesNative implements the static Storages API against the session's
// existing project storage. Storage-independent path helpers remain available
// to headless sessions without mounted project storage.
func (s *Services) storagesNative(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if operation == "@initialize" {
		return tjs.VoidValue, fmt.Errorf("Storages cannot be instantiated")
	}
	if operation == "setTextEncoding" {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Storages.setTextEncoding requires an encoding")
		}
		s.setScriptTextEncoding(args[0].Text())
		return tjs.VoidValue, nil
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
		if _, ok := lzfsStorageSource(name); ok {
			if !s.lzfsPluginLinked() {
				return tjs.VoidValue, lzfsUnavailable(name)
			}
			return tjs.StringValue(""), nil
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
		if _, ok := lzfsStorageSource(name); ok {
			data, err := s.readStorage(name)
			if err != nil {
				return tjs.VoidValue, err
			}
			return makeStorageSizeInfo(vm, uint64(len(data)))
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
	if operation == "setXP3ArchiveExtractionFilter" {
		if len(args) == 0 {
			return tjs.VoidValue, fmt.Errorf("Storages.setXP3ArchiveExtractionFilter requires a function or void")
		}
		callback := args[0]
		if callback.IsVoid() || (callback.Kind() == tjs.KindObject && callback.IsNull()) {
			s.mu.Lock()
			s.xp3ExtractionFilter = tjs.VoidValue
			s.mu.Unlock()
			if storage := storagesStorage(s); storage != nil {
				storage.SetExtractionFilter(nil)
			}
			return tjs.VoidValue, nil
		}
		if callback.Kind() != tjs.KindObject {
			return tjs.VoidValue, fmt.Errorf("Storages.setXP3ArchiveExtractionFilter requires a function or void")
		}
		object, exists := vm.Objects.Lookup(callback.ObjectRef().Object)
		if !exists || object.Category != tjs.CategoryFunction {
			return tjs.VoidValue, fmt.Errorf("Storages.setXP3ArchiveExtractionFilter requires a function or void")
		}
		s.mu.Lock()
		s.xp3ExtractionFilter = callback
		s.mu.Unlock()
		if storage := storagesStorage(s); storage != nil {
			storage.SetExtractionFilter(func(info assets.ExtractionFilterInfo) error {
				_, err := vm.CallFunction(callback, tjs.VoidValue, []tjs.Value{
					tjs.IntegerValue(int64(info.FileHash)),
					tjs.IntegerValue(int64(info.Offset)),
					tjs.OctetBufferValue(info.Buffer),
					tjs.IntegerValue(int64(len(info.Buffer))),
					tjs.StringValue(info.FileName),
					tjs.VoidValue,
				}, s, budget)
				return err
			})
		}
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
		if os.Getenv("KRKRGO_TRACE_ARCHIVE") != "" {
			log.Printf("archive-add name=%q full=%q mounted=%d", name, fullPath, len(storage.Archives))
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
		if _, ok := lzfsStorageSource(name); ok {
			if !s.lzfsPluginLinked() {
				return tjs.VoidValue, lzfsUnavailable(name)
			}
			return vm.NewArray(nil)
		}
		if !isVarStorage(name) || s == nil || s.varStorageVM == nil {
			return tjs.VoidValue, fmt.Errorf("Storages.getListAt supports only linked varfile storage paths")
		}
		return s.listVarStorage(vm, name)
	}
	if operation == "dirlist" || operation == "dirlistEx" {
		return s.storagesDirList(vm, operation, args)
	}
	if storagesFstatOperation(operation) {
		return s.storagesFstatNative(vm, operation, args)
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
		if _, ok := lzfsStorageSource(name); ok {
			if !s.lzfsPluginLinked() {
				return tjs.VoidValue, lzfsUnavailable(name)
			}
			return tjs.StringValue(name), nil
		}
		fullPath, err := storage.FullPath(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(fullPath), nil
	case "getPlacedPath":
		if source, ok := lzfsStorageSource(name); ok {
			if !s.lzfsPluginLinked() {
				return tjs.VoidValue, lzfsUnavailable(name)
			}
			if s.lzfsExists(source, true) {
				return tjs.StringValue(name), nil
			}
			return tjs.StringValue(""), nil
		}
		placedPath, err := storage.PlacedPath(name)
		if errors.Is(err, assets.ErrNotFound) {
			return tjs.StringValue(""), nil
		}
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(placedPath), nil
	case "isExistentStorage":
		if source, ok := lzfsStorageSource(name); ok {
			if s.lzfsExists(source, true) {
				return tjs.IntegerValue(1), nil
			}
			return tjs.IntegerValue(0), nil
		}
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
			save := s.storagesOverlaySave()
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

func makeStorageSizeInfo(vm *tjs.VM, size uint64) (tjs.Value, error) {
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
	if _, ok := lzfsStorageSource(name); ok {
		if !s.lzfsPluginLinked() {
			return tjs.VoidValue, lzfsUnavailable(name)
		}
		return vm.NewArray(nil)
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

// ErrStorageReadOnly reports a mutation of a packaged resource. The project
// storage and mounted XP3 archives are read-only; script writes belong to the
// save overlay, so methods that would modify the installation refuse instead
// of reporting success.
var ErrStorageReadOnly = errors.New("storage resource is read-only")

// Windows FILE_ATTRIBUTE bits exposed by fstat.dll. Only the portable subset
// (read-only plus the file-type bits) can be stored on the host filesystem;
// hidden, system, and temporary request an explicit unsupported-device error.
const (
	fileAttributeReadOnly  = 0x0001
	fileAttributeHidden    = 0x0002
	fileAttributeSystem    = 0x0004
	fileAttributeDirectory = 0x0010
	fileAttributeArchive   = 0x0020
	fileAttributeNormal    = 0x0080
	fileAttributeTemporary = 0x0100
)

// storagesFstatOperation reports whether operation belongs to the fstat.dll
// surface implemented by storagesFstatNative.
func storagesFstatOperation(operation string) bool {
	switch operation {
	case "clearStorageCaches", "getTime", "setTime", "getLastModifiedFileTime",
		"setLastModifiedFileTime", "exportFile", "deleteFile", "truncateFile",
		"moveFile", "dirtree", "removeDirectory", "createDirectory",
		"createDirectoryNoNormalize", "changeDirectory", "setFileAttributes",
		"resetFileAttributes", "getFileAttributes", "selectDirectory",
		"isExistentDirectory", "copyFile", "copyFileNoNormalize",
		"isExistentStorageNoSearchNoNormalize", "getDisplayName", "searchPath":
		return true
	default:
		return false
	}
}

// storagesFstatNative implements the fstat.dll Storages additions. Queries
// follow the reference plugin: they resolve project storage names through the
// storage containment rules and report local filesystem metadata. Mutations are
// routed to the save overlay so a packaged installation stays untouched, and
// archive members are rejected as read-only.
func (s *Services) storagesFstatNative(vm *tjs.VM, operation string, args []tjs.Value) (tjs.Value, error) {
	switch operation {
	case "clearStorageCaches":
		storage, err := s.storagesRequireStorage()
		if err != nil {
			return tjs.VoidValue, err
		}
		storage.ClearCache()
		return tjs.VoidValue, nil
	case "getTime":
		return s.storagesGetTime(vm, args)
	case "setTime":
		return s.storagesSetTime(vm, args)
	case "getLastModifiedFileTime":
		return s.storagesGetLastModifiedFileTime(args)
	case "setLastModifiedFileTime":
		return s.storagesSetLastModifiedFileTime(args)
	case "exportFile":
		return s.storagesExportFile(args)
	case "deleteFile":
		return s.storagesDeleteFile(args)
	case "truncateFile":
		return s.storagesTruncateFile(args)
	case "moveFile":
		return s.storagesMoveFile(args)
	case "dirtree":
		return s.storagesDirTree(vm, args)
	case "removeDirectory":
		return s.storagesRemoveDirectory(args)
	case "createDirectory":
		return s.storagesCreateDirectory(args, true)
	case "createDirectoryNoNormalize":
		return s.storagesCreateDirectory(args, false)
	case "changeDirectory":
		return s.storagesChangeDirectory(args)
	case "setFileAttributes":
		return s.storagesSetFileAttributes(args, false)
	case "resetFileAttributes":
		return s.storagesSetFileAttributes(args, true)
	case "getFileAttributes":
		return s.storagesGetFileAttributes(args)
	case "selectDirectory":
		return s.storagesSelectDirectory(args)
	case "isExistentDirectory":
		return s.storagesIsExistentDirectory(args)
	case "copyFile":
		return s.storagesCopyFile(args)
	case "copyFileNoNormalize":
		return s.storagesCopyFileNoNormalize(args)
	case "isExistentStorageNoSearchNoNormalize":
		return s.storagesIsExistentStorageNoSearchNoNormalize(args)
	case "getDisplayName":
		return s.storagesGetDisplayName(args)
	case "searchPath":
		return s.storagesSearchPath(args)
	default:
		return tjs.VoidValue, fmt.Errorf("unknown Storages operation: %s", operation)
	}
}

func (s *Services) storagesRequireStorage() (*assets.Storage, error) {
	storage := storagesStorage(s)
	if storage == nil {
		return nil, UnsupportedDevice("storage")
	}
	return storage, nil
}

// storagesLocalSource resolves a named resource to a local path. The save
// overlay is consulted first, then the project's loose files and directories.
// archive reports that the name addresses an XP3 member, which has no local
// filesystem path. A missing resource yields an empty path without an error.
func (s *Services) storagesLocalSource(name string) (normalized, path string, archive bool, err error) {
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return "", "", false, err
	}
	if _, ok := lzfsStorageSource(name); ok {
		if !s.lzfsPluginLinked() {
			return "", "", false, lzfsUnavailable(name)
		}
		return name, "", false, nil
	}
	normalized, err = storage.NormalizeStorageName(name)
	if err != nil {
		return "", "", false, err
	}
	if strings.ContainsRune(normalized, '>') {
		return normalized, "", true, nil
	}
	if s != nil && s.SaveDir != "" {
		if _, overlay, overlayErr := saveStoragePath(serviceProjectPath(s), s.SaveDir, normalized); overlayErr == nil {
			if info, statErr := os.Lstat(overlay); statErr == nil {
				if info.Mode()&os.ModeSymlink != 0 {
					return "", "", false, fmt.Errorf("%w: symlink target", ErrSavePath)
				}
				return normalized, overlay, false, nil
			} else if !os.IsNotExist(statErr) {
				return "", "", false, statErr
			}
		}
	}
	local, err := storage.LooseFile(normalized)
	if err != nil {
		return "", "", false, err
	}
	if local != "" {
		return normalized, local, false, nil
	}
	directory, err := storage.DirectoryPath(normalized)
	if err == nil {
		return normalized, directory, false, nil
	}
	if !errors.Is(err, assets.ErrNotFound) && !os.IsNotExist(err) {
		return "", "", false, err
	}
	return normalized, "", false, nil
}

// storagesPlacedLocalSource resolves a name the way TVPGetPlacedPath does:
// the save overlay and direct storage name are tried first, then registered
// search paths. It is used by copyFile, whose reference implementation accepts
// a source located through an auto-path but never extracts an archive member.
func (s *Services) storagesPlacedLocalSource(name string) (string, bool, error) {
	normalized, path, archive, err := s.storagesLocalSource(name)
	if err != nil || path != "" || archive {
		return path, archive, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return "", false, err
	}
	resolved, err := storage.Resolve(normalized)
	if err != nil {
		if errors.Is(err, assets.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if strings.ContainsRune(resolved, '>') {
		return "", true, nil
	}
	local, err := storage.LooseFile(resolved)
	if err != nil {
		return "", false, err
	}
	return local, false, nil
}

// storagesMutationTarget maps a normalized storage name to its save-overlay
// path. A name that exists only in the read-only project storage or in an
// archive is reported as ErrStorageReadOnly rather than being silently copied
// or recreated.
func (s *Services) storagesMutationTarget(storage *assets.Storage, name string) (string, bool, error) {
	if storage == nil {
		return "", false, UnsupportedDevice("storage")
	}
	normalized, err := storage.NormalizeStorageName(name)
	if err != nil {
		return "", false, err
	}
	if strings.ContainsRune(normalized, '>') {
		return "", false, fmt.Errorf("%w: archive member %s", ErrStorageReadOnly, normalized)
	}
	if s == nil || s.SaveDir == "" {
		return "", false, UnsupportedDevice("save storage")
	}
	_, path, err := saveStoragePath(serviceProjectPath(s), s.SaveDir, normalized)
	if err != nil {
		return "", false, err
	}
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("%w: symlink target", ErrSavePath)
		}
		return path, true, nil
	}
	if !os.IsNotExist(statErr) {
		return "", false, statErr
	}
	if exists, existsErr := storage.ExistsNoSearch(normalized); existsErr != nil {
		return "", false, existsErr
	} else if exists {
		return "", false, fmt.Errorf("%w: %s", ErrStorageReadOnly, normalized)
	}
	if directory, dirErr := storage.DirectoryPath(normalized); dirErr == nil && directory != "" {
		return "", false, fmt.Errorf("%w: %s", ErrStorageReadOnly, normalized)
	} else if dirErr != nil && !errors.Is(dirErr, assets.ErrNotFound) && !os.IsNotExist(dirErr) {
		return "", false, dirErr
	}
	return path, false, nil
}

// storagesWriteTarget maps a destination name to the save overlay. Unlike a
// mutation target, a destination may shadow a packaged resource: copying a
// file over a project or archive name creates an overlay entry without
// touching the installation.
func (s *Services) storagesWriteTarget(storage *assets.Storage, name string) (string, bool, error) {
	if storage == nil {
		return "", false, UnsupportedDevice("storage")
	}
	normalized, err := storage.NormalizeStorageName(name)
	if err != nil {
		return "", false, err
	}
	if strings.ContainsRune(normalized, '>') {
		return "", false, fmt.Errorf("%w: archive member %s", ErrStorageReadOnly, normalized)
	}
	if s == nil || s.SaveDir == "" {
		return "", false, UnsupportedDevice("save storage")
	}
	_, path, err := saveStoragePath(serviceProjectPath(s), s.SaveDir, normalized)
	if err != nil {
		return "", false, err
	}
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", false, fmt.Errorf("%w: symlink target", ErrSavePath)
		}
		return path, true, nil
	}
	if !os.IsNotExist(statErr) {
		return "", false, statErr
	}
	return path, false, nil
}

// storagesOverlayPath returns the save-overlay path reserved for name without
// requiring it to exist. It reports false when no save storage is configured.
func (s *Services) storagesOverlayPath(name string) (string, bool) {
	if s == nil || s.SaveDir == "" {
		return "", false
	}
	if name == "" {
		return s.SaveDir, true
	}
	_, path, err := saveStoragePath(serviceProjectPath(s), s.SaveDir, name)
	if err != nil {
		return "", false
	}
	return path, true
}

func (s *Services) storagesOverlaySave() *SaveStorage {
	if s == nil {
		return nil
	}
	return &SaveStorage{Root: s.SaveDir, Project: serviceProjectPath(s), Backend: s.SaveBackend}
}

// storagesTimeValue converts one dictionary timestamp. Integers are Unix
// seconds, matching dirlistEx and getLastModifiedFileTime; Date instances use
// their millisecond value, matching the reference plugin's Date round trip.
func (s *Services) storagesTimeValue(value tjs.Value, field string) (int64, bool, error) {
	if value.Kind() == tjs.KindVoid || (value.Kind() == tjs.KindObject && value.IsNull()) {
		return 0, false, nil
	}
	if value.Kind() == tjs.KindObject {
		id := value.ObjectRef().Object
		if s != nil && id != 0 {
			s.mu.RLock()
			date := s.Dates[id]
			s.mu.RUnlock()
			if date != nil {
				return date.GetTime() / 1000, true, nil
			}
		}
		return 0, false, fmt.Errorf("Storages %s timestamp must be Unix seconds or a Date", field)
	}
	seconds, err := value.Int()
	if err != nil {
		return 0, false, fmt.Errorf("Storages %s timestamp must be Unix seconds or a Date: %w", field, err)
	}
	return seconds, true, nil
}

func storagesDictionaryField(vm *tjs.VM, dict tjs.Value, field string) (tjs.Value, error) {
	if dict.Kind() != tjs.KindObject || dict.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Storages.setTime requires a timestamp dictionary")
	}
	object, ok := vm.Objects.Lookup(dict.ObjectRef().Object)
	if !ok {
		return tjs.VoidValue, fmt.Errorf("Storages.setTime timestamp dictionary is unavailable")
	}
	value, _ := object.Get(field)
	return value, nil
}

// storagesDateValue constructs a TJS Date for one Unix-second timestamp. The
// reference plugin stores Date instances in getTime dictionaries, so scripts
// may call the usual Date methods on the result. The instance is created with
// the class's default initializer and then pinned to the requested millisecond
// value, because a single numeric Date constructor argument means a year in
// this TJS implementation.
func (s *Services) storagesDateValue(vm *tjs.VM, unixSeconds int64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("Storages timestamps require a live TJS VM")
	}
	class, ok := vm.Globals["Date"]
	if !ok || class.Kind() != tjs.KindObject || class.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Storages timestamps require the registered Date class")
	}
	instance, err := vm.Construct(class, nil, s, nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	id := instance.ObjectRef().Object
	s.mu.Lock()
	date := s.Dates[id]
	s.mu.Unlock()
	if date == nil {
		return tjs.VoidValue, fmt.Errorf("Storages timestamps require a Date native instance")
	}
	if err := date.SetTime(unixSeconds * 1000); err != nil {
		return tjs.VoidValue, err
	}
	return instance, nil
}

func (s *Services) storagesGetTime(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "getTime")
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, path, archive, err := s.storagesLocalSource(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if archive {
		return tjs.VoidValue, fmt.Errorf("%w: archive member %s has no local timestamps", ErrStorageReadOnly, normalized)
	}
	if path == "" {
		return tjs.VoidValue, fmt.Errorf("%w: %s", assets.ErrNotFound, normalized)
	}
	info, err := os.Stat(path)
	if err != nil {
		return tjs.VoidValue, err
	}
	mtime, atime, ctime := assets.FileTimes(info)
	dict, err := tjsDictionary(vm)
	if err != nil {
		return tjs.VoidValue, err
	}
	for _, field := range []struct {
		name  string
		value int64
	}{
		{"mtime", mtime},
		{"ctime", ctime},
		{"atime", atime},
	} {
		date, err := s.storagesDateValue(vm, field.value)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Storages.getTime %s: %w", field.name, err)
		}
		if err := dict.Set(field.name, date); err != nil {
			return tjs.VoidValue, fmt.Errorf("Storages.getTime %s: %w", field.name, err)
		}
	}
	return tjs.ObjectValue(dict.Ref()), nil
}

func (s *Services) storagesSetTime(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.setTime requires a storage name and timestamp dictionary")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	dict := args[1]
	if dict.Kind() != tjs.KindObject || dict.IsNull() {
		return tjs.VoidValue, fmt.Errorf("Storages.setTime requires a timestamp dictionary")
	}
	path, exists, err := s.storagesMutationTarget(storage, args[0].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if !exists {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	mtimeValue, err := storagesDictionaryField(vm, dict, "mtime")
	if err != nil {
		return tjs.VoidValue, err
	}
	atimeValue, err := storagesDictionaryField(vm, dict, "atime")
	if err != nil {
		return tjs.VoidValue, err
	}
	mtimeSeconds, hasMtime, err := s.storagesTimeValue(mtimeValue, "mtime")
	if err != nil {
		return tjs.VoidValue, err
	}
	atimeSeconds, hasAtime, err := s.storagesTimeValue(atimeValue, "atime")
	if err != nil {
		return tjs.VoidValue, err
	}
	if !hasMtime && !hasAtime {
		return tjs.VoidValue, fmt.Errorf("Storages.setTime requires an mtime or atime field; creation times cannot be set on portable filesystems")
	}
	mtime, atime, _ := assets.FileTimes(info)
	if hasMtime {
		mtime = mtimeSeconds
	}
	if hasAtime {
		atime = atimeSeconds
	}
	if err := os.Chtimes(path, time.Unix(atime, 0), time.Unix(mtime, 0)); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesGetLastModifiedFileTime(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "getLastModifiedFileTime")
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, path, archive, err := s.storagesLocalSource(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if archive {
		return tjs.VoidValue, fmt.Errorf("%w: archive member %s has no local modification time", ErrStorageReadOnly, normalized)
	}
	if path == "" {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(info.ModTime().Unix()), nil
}

func (s *Services) storagesSetLastModifiedFileTime(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.setLastModifiedFileTime requires a storage name and time")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	seconds, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Storages.setLastModifiedFileTime time must be Unix seconds: %w", err)
	}
	path, exists, err := s.storagesMutationTarget(storage, args[0].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if !exists {
		return tjs.IntegerValue(0), nil
	}
	when := time.Unix(seconds, 0)
	if err := os.Chtimes(path, when, when); err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesExportFile(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.exportFile requires a source and destination storage name")
	}
	source, destination := args[0].Text(), args[1].Text()
	if strings.ContainsRune(destination, '>') {
		return tjs.VoidValue, fmt.Errorf("%w: archive members cannot be written", ErrStorageReadOnly)
	}
	data, err := s.readStorage(source)
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Storages.exportFile cannot open readfile %s: %w", source, err)
	}
	if err := s.writeSave(destination, data, 0); err != nil {
		return tjs.VoidValue, fmt.Errorf("Storages.exportFile cannot open storefile %s: %w", destination, err)
	}
	return tjs.VoidValue, nil
}

func (s *Services) storagesDeleteFile(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "deleteFile")
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	path, exists, err := s.storagesMutationTarget(storage, name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !exists {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	if !info.Mode().IsRegular() {
		return tjs.IntegerValue(0), nil
	}
	if err := os.Remove(path); err != nil {
		return tjs.IntegerValue(0), nil
	}
	storage.ClearCache()
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesTruncateFile(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.truncateFile requires a storage name and size")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	size, err := args[1].Int()
	if err != nil {
		return tjs.VoidValue, fmt.Errorf("Storages.truncateFile size must be an integer: %w", err)
	}
	if size < 0 {
		return tjs.VoidValue, fmt.Errorf("Storages.truncateFile size must not be negative")
	}
	path, exists, err := s.storagesMutationTarget(storage, args[0].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if !exists {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	if !info.Mode().IsRegular() {
		return tjs.IntegerValue(0), nil
	}
	if err := os.Truncate(path, size); err != nil {
		return tjs.IntegerValue(0), nil
	}
	storage.ClearCache()
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesMoveFile(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.moveFile requires a source and destination storage name")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	fromNormalized, fromPath, fromArchive, err := s.storagesLocalSource(args[0].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if fromArchive || fromPath == "" {
		return tjs.IntegerValue(0), nil
	}
	fromPath, fromExists, err := s.storagesMutationTarget(storage, fromNormalized)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !fromExists {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(fromPath)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	if !info.Mode().IsRegular() {
		return tjs.IntegerValue(0), nil
	}
	toPath, _, err := s.storagesWriteTarget(storage, args[1].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if filepath.Clean(fromPath) == filepath.Clean(toPath) {
		return tjs.IntegerValue(0), nil
	}
	if err := s.storagesCopyLocalFile(fromPath, toPath); err != nil {
		return tjs.IntegerValue(0), nil
	}
	if err := os.Remove(fromPath); err != nil {
		return tjs.IntegerValue(0), nil
	}
	storage.ClearCache()
	return tjs.IntegerValue(1), nil
}

// storagesCopyLocalFile streams from one local filesystem path to one save
// overlay path, creating the destination parent with the overlay's symlink
// checks. The destination is truncated, matching the reference TVPCopyFile.
func (s *Services) storagesCopyLocalFile(from, to string) error {
	save := s.storagesOverlaySave()
	if save == nil || save.Root == "" {
		return UnsupportedDevice("save storage")
	}
	if err := save.ensureParent(to); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// storagesWriteOverlayFile writes data to an already-contained overlay path.
func (s *Services) storagesWriteOverlayFile(path string, data []byte) error {
	save := s.storagesOverlaySave()
	if save == nil || save.Root == "" {
		return UnsupportedDevice("save storage")
	}
	if err := save.ensureParent(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o666)
}

// rawSavePath resolves a storage name without storage-name normalization while
// still confining the result to the save overlay (absolute names inside the
// project are rebased onto the overlay).
func rawSavePath(project, root, name string) (string, error) {
	if root == "" {
		return "", UnsupportedDevice("save storage")
	}
	if strings.ContainsRune(name, '>') {
		return "", fmt.Errorf("%w: archive members cannot be written", ErrStorageReadOnly)
	}
	candidate := core.LocalStorageNativePath(strings.ReplaceAll(name, `\`, "/"))
	path := filepath.FromSlash(candidate)
	if filepath.IsAbs(path) {
		matched := false
		for _, base := range []string{root, project} {
			if base == "" {
				continue
			}
			relative, ok, err := core.LocalStorageRelativePath(candidate, base)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidSaveName, err)
			}
			if !ok {
				continue
			}
			path = filepath.Join(root, filepath.FromSlash(relative))
			matched = true
			break
		}
		if !matched {
			return "", fmt.Errorf("%w: %q is outside the save and project directories", ErrInvalidSaveName, name)
		}
	} else {
		path = filepath.Join(root, path)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%w: %q", ErrInvalidSaveName, name)
	}
	return pathAbs, nil
}

func (s *Services) storagesDirTree(vm *tjs.VM, args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "dirtree")
	if err != nil {
		return tjs.VoidValue, err
	}
	dirOnly := false
	if len(args) > 1 {
		dirOnly, err = args[1].Truth()
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Storages.dirtree directory-only flag must be a value: %w", err)
		}
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, err := storage.NormalizeStorageName(name + "/")
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.ContainsRune(normalized, '>') {
		return tjs.VoidValue, fmt.Errorf("archive directories cannot be enumerated: %s", normalized)
	}
	base := ""
	if directory, err := storage.DirectoryPath(normalized); err == nil {
		base = directory
	} else if !errors.Is(err, assets.ErrNotFound) && !os.IsNotExist(err) {
		return tjs.VoidValue, err
	}
	overlay, _ := s.storagesOverlayPath(normalized)
	// A file occupying either root is not a tree; the reference reports an
	// empty array for a non-directory instead of an enumeration error.
	if base != "" {
		if info, err := os.Stat(base); err != nil || !info.IsDir() {
			base = ""
		}
	}
	if overlay != "" {
		if info, err := os.Lstat(overlay); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			overlay = ""
		}
	}
	if base == "" && overlay == "" {
		return vm.NewArray(nil)
	}
	values := make([]tjs.Value, 0)
	if err := storagesTreeWalk(base, overlay, "", dirOnly, &values); err != nil {
		return tjs.VoidValue, err
	}
	return vm.NewArray(values)
}

type storagesTreeNode struct {
	baseName    string
	overlayName string
	isDir       bool
}

// storagesTreeWalk merges one read-only project directory with its save-overlay
// counterpart. Entries are ordered by case-folded name for deterministic
// replays; an overlay entry shadows a project entry with the same name and
// provides the reported spelling. Symbolic links are skipped so a tree listing
// cannot escape the project or loop.
func storagesTreeWalk(base, overlay, prefix string, dirOnly bool, values *[]tjs.Value) error {
	nodes := make(map[string]storagesTreeNode)
	if base != "" {
		entries, err := os.ReadDir(base)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			nodes[strings.ToLower(entry.Name())] = storagesTreeNode{baseName: entry.Name(), isDir: entry.IsDir()}
		}
	}
	if overlay != "" {
		entries, err := os.ReadDir(overlay)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			node := nodes[strings.ToLower(entry.Name())]
			node.overlayName = entry.Name()
			node.isDir = entry.IsDir()
			nodes[strings.ToLower(entry.Name())] = node
		}
	}
	keys := make([]string, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node := nodes[key]
		name := node.baseName
		if node.overlayName != "" {
			name = node.overlayName
		}
		if node.isDir {
			full := prefix + name + "/"
			*values = append(*values, tjs.StringValue(full))
			childBase, childOverlay := "", ""
			if base != "" && node.baseName != "" {
				childBase = filepath.Join(base, node.baseName)
			}
			if overlay != "" && node.overlayName != "" {
				childOverlay = filepath.Join(overlay, node.overlayName)
			}
			if err := storagesTreeWalk(childBase, childOverlay, full, dirOnly, values); err != nil {
				return err
			}
			continue
		}
		if !dirOnly {
			*values = append(*values, tjs.StringValue(prefix+name))
		}
	}
	return nil
}

func storagesRequireDirectoryName(args []tjs.Value, operation string) (string, error) {
	name, err := storagesPathArg(args, operation)
	if err != nil {
		return "", err
	}
	if name == "" || (name[len(name)-1] != '/' && name[len(name)-1] != '\\') {
		return "", fmt.Errorf("Storages.%s: '/' must be specified at the end of given directory name.", operation)
	}
	return name, nil
}

func (s *Services) storagesRemoveDirectory(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesRequireDirectoryName(args, "removeDirectory")
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, err := storage.NormalizeStorageName(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.ContainsRune(normalized, '>') {
		return tjs.VoidValue, fmt.Errorf("%w: archive directory %s", ErrStorageReadOnly, normalized)
	}
	path, ok := s.storagesOverlayPath(normalized)
	if !ok {
		return tjs.VoidValue, UnsupportedDevice("save storage")
	}
	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return tjs.VoidValue, fmt.Errorf("%w: symlink target", ErrSavePath)
		}
		if !info.IsDir() {
			return tjs.IntegerValue(0), nil
		}
		// A non-empty directory fails, as the reference unlink does; the
		// directory stays intact and the call reports false.
		if err := os.Remove(path); err != nil {
			return tjs.IntegerValue(0), nil
		}
		storage.ClearCache()
		return tjs.IntegerValue(1), nil
	}
	if !os.IsNotExist(statErr) {
		return tjs.VoidValue, statErr
	}
	if directory, err := storage.DirectoryPath(normalized); err == nil && directory != "" {
		return tjs.VoidValue, fmt.Errorf("%w: project directory %s", ErrStorageReadOnly, normalized)
	}
	return tjs.IntegerValue(0), nil
}

func (s *Services) storagesCreateDirectory(args []tjs.Value, normalize bool) (tjs.Value, error) {
	operation := "createDirectory"
	if !normalize {
		operation = "createDirectoryNoNormalize"
	}
	name, err := storagesRequireDirectoryName(args, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	if s == nil || s.SaveDir == "" {
		return tjs.VoidValue, UnsupportedDevice("save storage")
	}
	var path string
	if normalize {
		normalized, err := storage.NormalizeStorageName(name)
		if err != nil {
			return tjs.VoidValue, err
		}
		if strings.ContainsRune(normalized, '>') {
			return tjs.VoidValue, fmt.Errorf("%w: archive directory %s", ErrStorageReadOnly, normalized)
		}
		overlay, ok := s.storagesOverlayPath(normalized)
		if !ok {
			return tjs.VoidValue, UnsupportedDevice("save storage")
		}
		path = overlay
	} else {
		rawPath, err := rawSavePath(serviceProjectPath(s), s.SaveDir, name)
		if err != nil {
			return tjs.VoidValue, err
		}
		path = rawPath
	}
	created, err := storagesCreateOverlayDirectory(s, path)
	if err != nil {
		if errors.Is(err, ErrSavePath) {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(0), nil
	}
	if !created {
		return tjs.IntegerValue(0), nil
	}
	return tjs.IntegerValue(1), nil
}

// storagesCreateOverlayDirectory creates one directory tree below the save
// overlay. It reports created=false when the target already exists and returns
// ErrSavePath when a non-directory or symlink occupies the target.
func storagesCreateOverlayDirectory(s *Services, path string) (bool, error) {
	save := s.storagesOverlaySave()
	if save == nil || save.Root == "" {
		return false, UnsupportedDevice("save storage")
	}
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("%w: %q exists and is not a directory", ErrSavePath, path)
		}
		return false, nil
	}
	if !os.IsNotExist(err) {
		return false, err
	}
	if err := save.ensureParent(path); err != nil {
		return false, err
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Services) storagesChangeDirectory(args []tjs.Value) (tjs.Value, error) {
	if _, err := storagesRequireDirectoryName(args, "changeDirectory"); err != nil {
		return tjs.VoidValue, err
	}
	// The Go storage model resolves every name against the project root and
	// the save overlay; there is no process working directory to change.
	return tjs.VoidValue, UnsupportedDevice("Storages.changeDirectory: storage names are root-relative")
}

// storagesAttributeBits projects a host file mode onto the portable subset of
// Windows FILE_ATTRIBUTE bits: the file-type bits and read-only.
func storagesAttributeBits(info os.FileInfo) int64 {
	attrib := int64(0)
	switch {
	case info.IsDir():
		attrib |= fileAttributeDirectory
	case info.Mode().IsRegular():
		attrib |= fileAttributeArchive
	}
	if info.Mode().Perm()&0o222 == 0 {
		attrib |= fileAttributeReadOnly
	}
	if attrib == 0 {
		attrib = fileAttributeNormal
	}
	return attrib
}

func storagesFileAttributeArg(args []tjs.Value, operation string) (int64, error) {
	if len(args) < 2 {
		return 0, fmt.Errorf("Storages.%s requires a storage name and attribute set", operation)
	}
	attrib, err := args[1].Int()
	if err != nil {
		return 0, fmt.Errorf("Storages.%s attribute must be an integer: %w", operation, err)
	}
	const unsupported = fileAttributeHidden | fileAttributeSystem | fileAttributeTemporary
	if attrib&unsupported != 0 {
		return 0, UnsupportedDevice("file attributes hidden/system/temporary")
	}
	return attrib, nil
}

func (s *Services) storagesSetFileAttributes(args []tjs.Value, reset bool) (tjs.Value, error) {
	operation := "setFileAttributes"
	if reset {
		operation = "resetFileAttributes"
	}
	attrib, err := storagesFileAttributeArg(args, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	name, err := storagesPathArg(args, operation)
	if err != nil {
		return tjs.VoidValue, err
	}
	path, exists, err := s.storagesMutationTarget(storage, name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if !exists {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	perm := info.Mode().Perm()
	readOnly := attrib&fileAttributeReadOnly != 0
	switch {
	case reset && readOnly:
		if perm&0o200 == 0 {
			perm |= 0o200
		}
	case !reset && readOnly:
		perm &^= 0o222
	case !reset && perm&0o222 == 0:
		perm |= 0o200
	}
	if perm != info.Mode().Perm() {
		special := info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
		if err := os.Chmod(path, special|perm); err != nil {
			return tjs.VoidValue, err
		}
	}
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesGetFileAttributes(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "getFileAttributes")
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, path, archive, err := s.storagesLocalSource(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if archive {
		return tjs.VoidValue, fmt.Errorf("%w: archive member %s has no local attributes", ErrStorageReadOnly, normalized)
	}
	if path == "" {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(storagesAttributeBits(info)), nil
}

func (s *Services) storagesSelectDirectory(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("Storages.selectDirectory requires a window")
	}
	return tjs.VoidValue, UnsupportedDevice("Storages.selectDirectory: interactive directory dialog")
}

func (s *Services) storagesIsExistentDirectory(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesRequireDirectoryName(args, "isExistentDirectory")
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, err := storage.NormalizeStorageName(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if strings.ContainsRune(normalized, '>') {
		return tjs.IntegerValue(0), nil
	}
	if path, ok := s.storagesOverlayPath(normalized); ok {
		if info, err := os.Lstat(path); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return tjs.VoidValue, fmt.Errorf("%w: symlink target", ErrSavePath)
			}
			if info.IsDir() {
				return tjs.IntegerValue(1), nil
			}
		}
	}
	directory, err := storage.DirectoryPath(normalized)
	if err != nil {
		if errors.Is(err, assets.ErrNotFound) || os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	if directory == "" {
		return tjs.IntegerValue(0), nil
	}
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesCopyFile(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.copyFile requires a source and destination storage name")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	// The reference plugin copies only locally accessible sources; an archive
	// member reports false rather than being extracted implicitly.
	fromPath, fromArchive, err := s.storagesPlacedLocalSource(args[0].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if fromArchive || fromPath == "" {
		return tjs.IntegerValue(0), nil
	}
	info, err := os.Stat(fromPath)
	if err != nil {
		if os.IsNotExist(err) {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, err
	}
	if !info.Mode().IsRegular() {
		return tjs.IntegerValue(0), nil
	}
	toPath, toExists, err := s.storagesWriteTarget(storage, args[1].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if toExists {
		if toInfo, err := os.Stat(toPath); err == nil && !toInfo.Mode().IsRegular() {
			return tjs.IntegerValue(0), nil
		}
	}
	if filepath.Clean(fromPath) == filepath.Clean(toPath) {
		return tjs.IntegerValue(1), nil
	}
	if err := s.storagesCopyLocalFile(fromPath, toPath); err != nil {
		return tjs.IntegerValue(0), nil
	}
	storage.ClearCache()
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesCopyFileNoNormalize(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 2 {
		return tjs.VoidValue, fmt.Errorf("Storages.copyFileNoNormalize requires a source and destination storage name")
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	failIfExist := false
	if len(args) > 2 {
		failIfExist, err = args[2].Truth()
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Storages.copyFileNoNormalize fail-if-exist flag must be a value: %w", err)
		}
	}
	// Sources keep the storage search-path resolution of TVPGetPlacedPath.
	from := args[0].Text()
	fromPath := ""
	if s.SaveDir != "" {
		if _, overlay, err := saveStoragePath(serviceProjectPath(s), s.SaveDir, from); err == nil {
			if info, statErr := os.Lstat(overlay); statErr == nil &&
				info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() {
				fromPath = overlay
			}
		}
	}
	if fromPath == "" {
		resolved, err := storage.Resolve(from)
		if err != nil {
			return tjs.IntegerValue(0), nil
		}
		if strings.ContainsRune(resolved, '>') {
			return tjs.IntegerValue(0), nil
		}
		local, err := storage.LooseFile(resolved)
		if err != nil {
			return tjs.VoidValue, err
		}
		if local == "" {
			return tjs.IntegerValue(0), nil
		}
		info, err := os.Stat(local)
		if err != nil || !info.Mode().IsRegular() {
			return tjs.IntegerValue(0), nil
		}
		fromPath = local
	}
	toPath, err := rawSavePath(serviceProjectPath(s), s.SaveDir, args[1].Text())
	if err != nil {
		return tjs.VoidValue, err
	}
	if failIfExist {
		if _, err := os.Lstat(toPath); err == nil {
			return tjs.IntegerValue(0), nil
		} else if !os.IsNotExist(err) {
			return tjs.VoidValue, err
		}
	}
	if filepath.Clean(fromPath) == filepath.Clean(toPath) {
		return tjs.IntegerValue(1), nil
	}
	if err := s.storagesCopyLocalFile(fromPath, toPath); err != nil {
		return tjs.IntegerValue(0), nil
	}
	storage.ClearCache()
	return tjs.IntegerValue(1), nil
}

func (s *Services) storagesIsExistentStorageNoSearchNoNormalize(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "isExistentStorageNoSearchNoNormalize")
	if err != nil {
		return tjs.VoidValue, err
	}
	storage, err := s.storagesRequireStorage()
	if err != nil {
		return tjs.VoidValue, err
	}
	if source, ok := lzfsStorageSource(name); ok {
		if s.lzfsExists(source, false) {
			return tjs.IntegerValue(1), nil
		}
		return tjs.IntegerValue(0), nil
	}
	if s.SaveDir != "" {
		save := s.storagesOverlaySave()
		if exists, err := save.Exists(name); err == nil && exists {
			return tjs.IntegerValue(1), nil
		}
	}
	exists, err := storage.ExistsNoSearch(name)
	if err != nil {
		return tjs.IntegerValue(0), nil
	}
	if exists {
		return tjs.IntegerValue(1), nil
	}
	return tjs.IntegerValue(0), nil
}

func (s *Services) storagesGetDisplayName(args []tjs.Value) (tjs.Value, error) {
	name, err := storagesPathArg(args, "getDisplayName")
	if err != nil {
		return tjs.VoidValue, err
	}
	normalized, path, archive, err := s.storagesLocalSource(name)
	if err != nil {
		return tjs.VoidValue, err
	}
	if archive {
		return tjs.VoidValue, fmt.Errorf("%w: archive member %s has no local display name", ErrStorageReadOnly, normalized)
	}
	if path == "" {
		return tjs.StringValue(""), nil
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return tjs.StringValue(""), nil
		}
		return tjs.VoidValue, err
	}
	return tjs.StringValue(filepath.Base(path)), nil
}

func (s *Services) storagesSearchPath(args []tjs.Value) (tjs.Value, error) {
	if len(args) < 1 {
		return tjs.VoidValue, fmt.Errorf("Storages.searchPath requires a file name")
	}
	filename := args[0].Text()
	if filename == "" {
		return tjs.VoidValue, nil
	}
	searchPath := ""
	if len(args) > 1 {
		searchPath = args[1].Text()
	}
	directories := make([]string, 0)
	if searchPath == "" {
		searchPath = os.Getenv("PATH")
	}
	for _, directory := range strings.Split(searchPath, string(os.PathListSeparator)) {
		if directory != "" {
			directories = append(directories, directory)
		}
	}
	candidate := filepath.FromSlash(filename)
	for _, directory := range directories {
		full := candidate
		if !filepath.IsAbs(full) {
			full = filepath.Join(filepath.FromSlash(directory), full)
		}
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		absolute, err := filepath.Abs(full)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.StringValue(core.LocalStorageName(filepath.Clean(absolute))), nil
	}
	return tjs.VoidValue, nil
}
