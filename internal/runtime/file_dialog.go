package runtime

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

var ErrFileDialogUnavailable = errors.New("file dialog handler unavailable")
var ErrFileDialogCancelled = errors.New("file dialog cancelled")

type FileDialogMode uint8

const (
	FileDialogOpen FileDialogMode = iota
	FileDialogSave
	FileDialogFolder
)

// FileDialogRequest contains no platform-specific widget types. Filters are
// host-defined display strings; the selected paths are always returned by the
// injected host handler rather than opened by the runtime.
type FileDialogRequest struct {
	Title       string         `json:"title,omitempty"`
	Mode        FileDialogMode `json:"mode"`
	InitialPath string         `json:"initial_path,omitempty"`
	Filter      string         `json:"filter,omitempty"`
	Multiple    bool           `json:"multiple,omitempty"`
	Suggested   string         `json:"suggested,omitempty"`
}
type FileDialogResult struct {
	Paths    []string `json:"paths,omitempty"`
	Accepted bool     `json:"accepted"`
}
type FileDialogHandler func(FileDialogRequest) (FileDialogResult, error)

type FileDialogService struct {
	mu      sync.RWMutex
	Handler FileDialogHandler
}

func NewFileDialogService(handler FileDialogHandler) *FileDialogService {
	return &FileDialogService{Handler: handler}
}
func (d *FileDialogService) SetHandler(handler FileDialogHandler) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.Handler = handler
	d.mu.Unlock()
}
func (d *FileDialogService) handler() FileDialogHandler {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	h := d.Handler
	d.mu.RUnlock()
	return h
}
func (d *FileDialogService) Show(request FileDialogRequest) (FileDialogResult, error) {
	h := d.handler()
	if h == nil {
		return FileDialogResult{}, ErrFileDialogUnavailable
	}
	if request.Mode > FileDialogFolder {
		return FileDialogResult{}, fmt.Errorf("invalid file dialog mode %d", request.Mode)
	}
	result, err := h(request)
	if err != nil {
		return FileDialogResult{}, err
	}
	for _, path := range result.Paths {
		if path == "" || strings.IndexByte(path, 0) >= 0 {
			return FileDialogResult{}, fmt.Errorf("invalid selected file path")
		}
	}
	if !request.Multiple && len(result.Paths) > 1 {
		return FileDialogResult{}, fmt.Errorf("file dialog returned multiple paths for single selection")
	}
	if len(result.Paths) == 0 && !result.Accepted {
		return result, ErrFileDialogCancelled
	}
	result.Paths = append([]string(nil), result.Paths...)
	return result, nil
}
func (d *FileDialogService) Open(request FileDialogRequest) (FileDialogResult, error) {
	request.Mode = FileDialogOpen
	return d.Show(request)
}
func (d *FileDialogService) Save(request FileDialogRequest) (FileDialogResult, error) {
	request.Mode = FileDialogSave
	return d.Show(request)
}
func (d *FileDialogService) ChooseFolder(request FileDialogRequest) (FileDialogResult, error) {
	request.Mode = FileDialogFolder
	return d.Show(request)
}
func (d *FileDialogService) Handle(request FileDialogRequest) (FileDialogResult, error) {
	return d.Show(request)
}
