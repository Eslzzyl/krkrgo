// Package krkrgo exposes the Go host used by the Android launcher.
package krkrgo

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/host"
	"github.com/eslzzyl/krkrgo/internal/krkrgocli"
	"github.com/hajimehoshi/ebiten/v2"
)

// ContentURIReader is implemented by the Android launcher. It gives the Go
// runtime random access to a persisted Storage Access Framework tree URI.
type ContentURIReader interface {
	Open(path string) (int64, error)
	Size(handle int64) (int64, error)
	Read(handle int64, offset int64, size int64) ([]byte, error)
	Close(handle int64)
	List(path string) (string, error)
	Stat(path string) (int64, error)
	Mkdir(path string) error
	Write(path string, offset int64, data []byte) error
	Delete(path string) error
}

type contentBackend struct{ reader ContentURIReader }

type contentFile struct {
	reader ContentURIReader
	handle int64
}

func (f *contentFile) ReadAt(data []byte, offset int64) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	bytes, err := f.reader.Read(f.handle, offset, int64(len(data)))
	copy(data, bytes)
	if err != nil {
		return len(bytes), err
	}
	if len(bytes) != len(data) {
		return len(bytes), io.ErrUnexpectedEOF
	}
	return len(bytes), nil
}

func (f *contentFile) Close() error {
	if f == nil || f.reader == nil {
		return nil
	}
	f.reader.Close(f.handle)
	f.reader = nil
	return nil
}

func (b contentBackend) Open(path string) (assets.ContentFile, int64, error) {
	handle, err := b.reader.Open(path)
	if err != nil {
		return nil, 0, err
	}
	size, err := b.reader.Size(handle)
	if err != nil {
		b.reader.Close(handle)
		return nil, 0, err
	}
	return &contentFile{reader: b.reader, handle: handle}, size, nil
}

func (b contentBackend) ReadDir(path string) ([]assets.ContentEntry, error) {
	encoded, err := b.reader.List(path)
	if err != nil {
		return nil, err
	}
	names := strings.Split(encoded, "\x00")
	if encoded == "" {
		names = nil
	}
	entries := make([]assets.ContentEntry, 0, len(names))
	for _, record := range names {
		if record == "" {
			continue
		}
		isDir := record[0] == 'D'
		if !isDir && record[0] != 'F' {
			return nil, fmt.Errorf("invalid content directory record")
		}
		name := record[1:]
		mode := os.FileMode(0444)
		if isDir {
			mode |= os.ModeDir
		}
		entries = append(entries, assets.ContentEntry{Name: name, IsDir: isDir, Mode: mode})
	}
	return entries, nil
}

func (b contentBackend) Stat(path string) (assets.ContentEntry, error) {
	stat, err := b.reader.Stat(path)
	if err != nil {
		return assets.ContentEntry{}, err
	}
	entry := assets.ContentEntry{Name: path, Mode: os.FileMode(0444)}
	if stat < 0 {
		entry.IsDir = true
		entry.Mode |= os.ModeDir
	} else {
		entry.Size = stat
	}
	return entry, nil
}

type contentSaveBackend struct {
	reader ContentURIReader
	root   string
	mu     sync.RWMutex
	files  map[string]struct{}
	ready  bool
}

func (b *contentSaveBackend) path(name string) string {
	name = strings.Trim(name, "/")
	if name == "" {
		return b.root
	}
	return b.root + "/" + name
}

func (b *contentSaveBackend) Read(name string) ([]byte, error) {
	handle, err := b.reader.Open(b.path(name))
	if err != nil {
		return nil, err
	}
	defer b.reader.Close(handle)
	size, err := b.reader.Size(handle)
	if err != nil {
		return nil, err
	}
	if size < 0 || size > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("save file size is invalid: %d", size)
	}
	data := make([]byte, int(size))
	for offset := int64(0); offset < size; {
		chunk, err := b.reader.Read(handle, offset, size-offset)
		if err != nil {
			return nil, err
		}
		if len(chunk) == 0 {
			return nil, io.ErrUnexpectedEOF
		}
		copy(data[offset:], chunk)
		offset += int64(len(chunk))
	}
	return data, nil
}

func (b *contentSaveBackend) Write(name string, data []byte, offset int64) error {
	if err := b.reader.Write(b.path(name), offset, data); err != nil {
		return err
	}
	b.mu.Lock()
	if b.files == nil {
		b.files = make(map[string]struct{})
	}
	b.files[name] = struct{}{}
	b.ready = true
	b.mu.Unlock()
	return nil
}

func (b *contentSaveBackend) Delete(name string) error {
	if err := b.reader.Delete(b.path(name)); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.files, name)
	b.mu.Unlock()
	return nil
}

func (b *contentSaveBackend) Exists(name string) (bool, error) {
	b.mu.RLock()
	ready := b.ready
	_, exists := b.files[name]
	b.mu.RUnlock()
	if ready {
		return exists, nil
	}
	files, err := b.listFiles("")
	if err != nil {
		return false, err
	}
	b.mu.Lock()
	if b.files == nil {
		b.files = make(map[string]struct{}, len(files))
	}
	for _, file := range files {
		b.files[file] = struct{}{}
	}
	b.ready = true
	_, exists = b.files[name]
	b.mu.Unlock()
	return exists, nil
}

func (b *contentSaveBackend) Mkdir(name string) error {
	return b.reader.Mkdir(b.path(name))
}

func (b *contentSaveBackend) List(prefix string) ([]string, error) {
	return b.listFiles(prefix)
}

func (b *contentSaveBackend) listFiles(prefix string) ([]string, error) {
	var files []string
	var visit func(string, string) error
	visit = func(path, relative string) error {
		encoded, err := b.reader.List(path)
		if err != nil {
			return err
		}
		if encoded == "" {
			return nil
		}
		for _, record := range strings.Split(encoded, "\x00") {
			if len(record) < 1 {
				continue
			}
			name := record[1:]
			childRelative := name
			if relative != "" {
				childRelative = relative + "/" + name
			}
			childPath := path + "/" + name
			if record[0] == 'D' {
				if err := visit(childPath, childRelative); err != nil {
					return err
				}
			} else if record[0] == 'F' {
				files = append(files, childRelative)
			}
		}
		return nil
	}
	path := b.path(prefix)
	relative := strings.Trim(prefix, "/")
	if err := visit(path, relative); err != nil {
		return nil, err
	}
	return files, nil
}

const (
	defaultBudget = ^uint64(0)
	defaultScale  = int32(1)
)

// controller owns the native game session and the Ebitengine presentation
// object. The Android UI controls it through the exported lifecycle methods.
type controller struct {
	mu      sync.RWMutex
	game    *host.Game
	paused  bool
	started bool
}

type mobileGame struct {
	owner *controller
}

var (
	globalController = &controller{}
	globalGame       = &mobileGame{owner: globalController}
)

// OpenGame loads a project and prepares the Ebitengine view.
func OpenGame(project string) error {
	return globalController.openGame(project)
}

// OpenGameWithContentURI opens a project directly from a content URI. Saves
// are written below the selected game's savedata directory.
func OpenGameWithContentURI(uri string, reader ContentURIReader) error {
	return globalController.openContentGame(uri, reader)
}

// PauseGame suspends runtime ticks while the Android view remains attached.
func PauseGame() {
	globalController.mu.Lock()
	globalController.paused = true
	globalController.mu.Unlock()
}

// ResumeGame resumes runtime ticks after the Android view becomes active.
func ResumeGame() {
	globalController.mu.Lock()
	if globalController.game != nil {
		globalController.paused = false
	}
	globalController.mu.Unlock()
}

// CloseGame releases the current runtime session and presentation resources.
func CloseGame() {
	globalController.closeGame()
}

func (c *controller) openGame(project string) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return fmt.Errorf("project path is empty")
	}

	session, err := krkrgocli.NewSessionWithScaleAndArguments(
		project,
		"",
		defaultBudget,
		defaultScale,
		nil,
	)
	if err != nil {
		return err
	}
	game := host.NewGameWithInitialWindowSize(session, host.DefaultWidth, host.DefaultHeight)

	c.mu.Lock()
	oldGame := c.game
	c.game = game
	c.paused = false
	c.started = true
	c.mu.Unlock()

	if oldGame != nil {
		oldSession := oldGame.Session()
		oldGame.Deallocate()
		if oldSession != nil {
			_ = oldSession.Close()
		}
	}
	return nil
}

func (c *controller) openContentGame(uri string, reader ContentURIReader) error {
	uri = strings.TrimSpace(uri)
	if uri == "" || reader == nil {
		return fmt.Errorf("content project URI and reader are required")
	}
	storage, err := assets.OpenContentProject(uri, contentBackend{reader: reader}, core.DefaultLimits())
	if err != nil {
		return fmt.Errorf("open content project: %w", err)
	}
	if err := reader.Mkdir("savedata"); err != nil {
		return fmt.Errorf("create game save directory: %w", err)
	}
	saveBackend := &contentSaveBackend{reader: reader, root: "savedata"}
	session, err := krkrgocli.NewSessionWithStorageAndSaveBackend(storage, strings.TrimRight(uri, "/")+"/savedata", saveBackend, defaultBudget, defaultScale, nil)
	if err != nil {
		return err
	}
	game := host.NewGameWithInitialWindowSize(session, host.DefaultWidth, host.DefaultHeight)

	c.mu.Lock()
	oldGame := c.game
	c.game = game
	c.paused = false
	c.started = true
	c.mu.Unlock()

	if oldGame != nil {
		oldSession := oldGame.Session()
		oldGame.Deallocate()
		if oldSession != nil {
			_ = oldSession.Close()
		}
	}
	return nil
}

func (c *controller) closeGame() {
	c.mu.Lock()
	game := c.game
	c.game = nil
	c.paused = false
	c.started = false
	c.mu.Unlock()

	if game == nil {
		return
	}
	session := game.Session()
	game.Deallocate()
	if session != nil {
		_ = session.Close()
	}
}

func (g *mobileGame) Update() error {
	if g == nil || g.owner == nil {
		return nil
	}
	g.owner.mu.RLock()
	defer g.owner.mu.RUnlock()
	if g.owner.paused || g.owner.game == nil {
		return nil
	}
	return g.owner.game.Update()
}

func (g *mobileGame) Draw(screen *ebiten.Image) {
	if g == nil || g.owner == nil {
		return
	}
	g.owner.mu.RLock()
	defer g.owner.mu.RUnlock()
	if g.owner.paused || g.owner.game == nil {
		return
	}
	g.owner.game.Draw(screen)
}

func (g *mobileGame) Layout(width, height int) (int, int) {
	if g == nil || g.owner == nil {
		return host.DefaultWidth, host.DefaultHeight
	}
	g.owner.mu.RLock()
	defer g.owner.mu.RUnlock()
	if g.owner.game == nil {
		return host.DefaultWidth, host.DefaultHeight
	}
	return g.owner.game.Layout(width, height)
}
