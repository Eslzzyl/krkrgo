//go:build darwin || linux

package ffmpeg

import (
	"fmt"

	"github.com/ebitengine/purego"
)

type sharedLibrary struct {
	path string
	handle uintptr
}

func openSharedLibrary(path string) (sharedLibrary, error) {
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return sharedLibrary{}, err
	}
	return sharedLibrary{path: path, handle: handle}, nil
}

func (l sharedLibrary) symbol(name string) (uintptr, error) {
	if l.handle == 0 {
		return 0, fmt.Errorf("library %q is closed", l.path)
	}
	return purego.Dlsym(l.handle, name)
}

func (l sharedLibrary) close() error {
	if l.handle == 0 {
		return nil
	}
	return purego.Dlclose(l.handle)
}
