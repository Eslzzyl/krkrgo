//go:build windows

package ffmpeg

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type sharedLibrary struct {
	path string
	dll  *windows.DLL
}

func openSharedLibrary(path string) (sharedLibrary, error) {
	flags := uintptr(0)
	if filepath.Base(path) != path {
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return sharedLibrary{}, err
		}
		path = absolutePath
		flags = windows.LOAD_WITH_ALTERED_SEARCH_PATH
	}
	handle, err := windows.LoadLibraryEx(path, 0, flags)
	if err != nil {
		return sharedLibrary{}, err
	}
	return sharedLibrary{path: path, dll: &windows.DLL{Name: path, Handle: handle}}, nil
}

func (l sharedLibrary) symbol(name string) (uintptr, error) {
	if l.dll == nil {
		return 0, fmt.Errorf("library %q is closed", l.path)
	}
	proc, err := l.dll.FindProc(name)
	if err != nil {
		return 0, err
	}
	return proc.Addr(), nil
}

func (l sharedLibrary) close() error {
	if l.dll == nil {
		return nil
	}
	return l.dll.Release()
}
