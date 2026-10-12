//go:build windows

package ffmpeg

import (
	"fmt"

	"golang.org/x/sys/windows"
)

type sharedLibrary struct {
	path string
	dll  *windows.DLL
}

func openSharedLibrary(path string) (sharedLibrary, error) {
	dll, err := windows.LoadDLL(path)
	if err != nil {
		return sharedLibrary{}, err
	}
	return sharedLibrary{path: path, dll: dll}, nil
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
