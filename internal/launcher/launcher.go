package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

const CatalogVersion = 1

// Game is a registered game and its launcher-specific settings.
type Game struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Project string `json:"project"`
	SaveDir string `json:"saveDir,omitempty"`
	Budget  uint64 `json:"budget,omitempty"`
}

// Catalog is the versioned, JSON-backed list of registered games.
type Catalog struct {
	Version int    `json:"version"`
	Games   []Game `json:"games"`
}

func EmptyCatalog() Catalog { return Catalog{Version: CatalogVersion, Games: []Game{}} }

func LoadCatalog(path string) (Catalog, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyCatalog(), nil
	}
	if err != nil {
		return Catalog{}, fmt.Errorf("read catalog: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(data, &c); err != nil {
		return Catalog{}, fmt.Errorf("decode catalog: %w", err)
	}
	if c.Version != CatalogVersion {
		return Catalog{}, fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	if c.Games == nil {
		c.Games = []Game{}
	}
	for i := range c.Games {
		p, err := filepath.Abs(c.Games[i].Project)
		if err == nil {
			if resolved, resolveErr := filepath.EvalSymlinks(p); resolveErr == nil {
				p = resolved
			}
			c.Games[i].Project = p
		}
		if c.Games[i].ID == "" {
			c.Games[i].ID = GameID(c.Games[i].Project)
		}
	}
	return c, nil
}

// SaveCatalog writes the catalog atomically in its containing directory.
func SaveCatalog(path string, c Catalog) error {
	if c.Version != CatalogVersion {
		return fmt.Errorf("unsupported catalog version %d", c.Version)
	}
	c.Games = append([]Game(nil), c.Games...)
	for i := range c.Games {
		if c.Games[i].ID == "" {
			c.Games[i].ID = GameID(c.Games[i].Project)
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create catalog directory: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".catalog-*.tmp")
	if err != nil {
		return fmt.Errorf("create catalog temp file: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write catalog: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace catalog: %w", err)
	}
	ok = true
	return nil
}

// GameID returns a stable identifier derived from a canonical project path.
func GameID(project string) string {
	sum := sha256.Sum256([]byte(project))
	return hex.EncodeToString(sum[:8])
}

// InspectProject canonicalizes a directory and accepts startup.tjs at the root,
// in data/, or inside XP3 archives mounted at either level.
func InspectProject(project string) (Game, error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return Game{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Game{}, fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Game{}, err
	}
	if !info.IsDir() {
		return Game{}, fmt.Errorf("project is not a directory: %s", abs)
	}
	if projectHasStartup(abs) {
		return Game{ID: GameID(abs), Title: filepath.Base(abs), Project: abs}, nil
	}
	dataDir := filepath.Join(abs, "data")
	if info, err := os.Stat(dataDir); err == nil && info.IsDir() && projectHasStartup(dataDir) {
		return Game{ID: GameID(abs), Title: filepath.Base(abs), Project: abs}, nil
	}
	return Game{}, fmt.Errorf("startup.tjs not found in %q, %q, or an XP3 archive in either directory", filepath.Join(abs, "startup.tjs"), filepath.Join(dataDir, "startup.tjs"))
}

func projectHasStartup(dir string) bool {
	storage, err := assets.OpenStorage(dir, core.DefaultLimits())
	if err != nil {
		return false
	}
	_, err = storage.Resolve("startup.tjs")
	for _, archive := range storage.Archives {
		_ = archive.Close()
	}
	return err == nil
}
