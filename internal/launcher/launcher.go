package launcher

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
)

// Renderer backends accepted by the game runtime.
const (
	RendererGPU = "gpu"
	RendererCPU = "cpu"
)

// Catalog sort modes.
const (
	SortManual = "manual"
	SortTitle  = "title"
	SortRecent = "recent"
	SortAdded  = "added"
)

// Game is a registered game and its launcher-specific settings.
type Game struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Project      string `json:"project"`
	SaveDir      string `json:"saveDir,omitempty"`
	Budget       uint64 `json:"budget,omitempty"`
	Renderer     string `json:"renderer,omitempty"`
	Cover        string `json:"cover,omitempty"`
	AddedAt      int64  `json:"addedAt,omitempty"`
	LastPlayedAt int64  `json:"lastPlayedAt,omitempty"`
	PlayTimeSec  int64  `json:"playTimeSec,omitempty"`
	LaunchCount  int    `json:"launchCount,omitempty"`
}

// Catalog is the JSON-backed list of registered games.
type Catalog struct {
	Games        []Game `json:"games"`
	SortMode     string `json:"sortMode,omitempty"`
	LastSelected string `json:"lastSelected,omitempty"`
}

func EmptyCatalog() Catalog { return Catalog{Games: []Game{}, SortMode: SortManual} }

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
	if c.Games == nil {
		c.Games = []Game{}
	}
	switch c.SortMode {
	case SortManual, SortTitle, SortRecent, SortAdded:
	default:
		c.SortMode = SortManual
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
		if c.Games[i].Renderer == "" {
			c.Games[i].Renderer = RendererGPU
		}
	}
	return c, nil
}

// SaveCatalog writes the catalog atomically in its containing directory.
func SaveCatalog(path string, c Catalog) error {
	c.Games = append([]Game(nil), c.Games...)
	for i := range c.Games {
		if c.Games[i].ID == "" {
			c.Games[i].ID = GameID(c.Games[i].Project)
		}
		if c.Games[i].Renderer == "" {
			c.Games[i].Renderer = RendererGPU
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

// Add registers g, rejecting a project that is already in the catalog.
func (c *Catalog) Add(g Game) (Game, error) {
	for _, existing := range c.Games {
		if existing.Project == g.Project {
			return Game{}, fmt.Errorf("project already added: %s", g.Project)
		}
	}
	if g.ID == "" {
		g.ID = GameID(g.Project)
	}
	if g.Renderer == "" {
		g.Renderer = RendererGPU
	}
	if g.AddedAt == 0 {
		g.AddedAt = time.Now().Unix()
	}
	c.Games = append(c.Games, g)
	return g, nil
}

// Remove deletes the game with id from the catalog.
func (c *Catalog) Remove(id string) bool {
	for i := range c.Games {
		if c.Games[i].ID == id {
			c.Games = append(c.Games[:i], c.Games[i+1:]...)
			return true
		}
	}
	return false
}

// Move shifts one game so that it lands before index to, as a list's
// Reorder reports it.
func (c *Catalog) Move(from, to int) {
	if from < 0 || from >= len(c.Games) || to < 0 || to > len(c.Games) {
		return
	}
	g := c.Games[from]
	c.Games = append(c.Games[:from], c.Games[from+1:]...)
	if to > from {
		to--
	}
	if to > len(c.Games) {
		to = len(c.Games)
	}
	c.Games = append(c.Games, Game{})
	copy(c.Games[to+1:], c.Games[to:])
	c.Games[to] = g
}

// Find returns the game with id, or nil.
func (c *Catalog) Find(id string) *Game {
	for i := range c.Games {
		if c.Games[i].ID == id {
			return &c.Games[i]
		}
	}
	return nil
}

// VisibleGames returns the catalog indices matching query, in the order
// sortMode asks for.
func VisibleGames(games []Game, query, sortMode string) []int {
	q := strings.ToLower(strings.TrimSpace(query))
	order := make([]int, 0, len(games))
	for i := range games {
		g := &games[i]
		if q == "" || strings.Contains(strings.ToLower(g.Title), q) || strings.Contains(strings.ToLower(g.Project), q) {
			order = append(order, i)
		}
	}
	title := func(i int) string { return strings.ToLower(games[i].Title) }
	switch sortMode {
	case SortTitle:
		sort.SliceStable(order, func(a, b int) bool { return title(order[a]) < title(order[b]) })
	case SortRecent:
		sort.SliceStable(order, func(a, b int) bool {
			left, right := games[order[a]], games[order[b]]
			if left.LastPlayedAt != right.LastPlayedAt {
				return left.LastPlayedAt > right.LastPlayedAt
			}
			return title(order[a]) < title(order[b])
		})
	case SortAdded:
		sort.SliceStable(order, func(a, b int) bool {
			left, right := games[order[a]], games[order[b]]
			if left.AddedAt != right.AddedAt {
				return left.AddedAt > right.AddedAt
			}
			return title(order[a]) < title(order[b])
		})
	}
	return order
}

// ValidateGame reports settings the runtime cannot accept.
func ValidateGame(g Game) error {
	if strings.TrimSpace(g.Project) == "" {
		return fmt.Errorf("project path is empty")
	}
	switch g.Renderer {
	case "", RendererGPU, RendererCPU:
	default:
		return fmt.Errorf("unsupported renderer %q", g.Renderer)
	}
	return nil
}

// ProjectAvailable reports whether project still resolves startup.tjs from a
// project directory or an explicit XP3 entry file.
func ProjectAvailable(project string) bool {
	info, err := os.Stat(project)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return projectHasStartup(project)
	}
	for _, dir := range []string{project, filepath.Join(project, "data")} {
		if regularFile(filepath.Join(dir, "startup.tjs")) {
			return true
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".xp3") {
				return true
			}
		}
	}
	return false
}

// DiscoverCover returns the first cover image found in the project
// directory or its data directory.
func DiscoverCover(project string) string {
	names := []string{"cover.png", "cover.jpg", "cover.jpeg", "icon.png", "icon.jpg", "logo.png"}
	project = projectDirectory(project)
	for _, dir := range []string{project, filepath.Join(project, "data")} {
		for _, name := range names {
			path := filepath.Join(dir, name)
			if regularFile(path) {
				return path
			}
		}
	}
	return ""
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// GameID returns a stable identifier derived from a canonical project path.
func GameID(project string) string {
	sum := sha256.Sum256([]byte(project))
	return hex.EncodeToString(sum[:8])
}

// InspectProject canonicalizes a project directory or an explicit XP3 entry
// file. Directory inputs are reduced to the XP3 containing startup.tjs when
// one exists; loose startup.tjs keeps the directory input.
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
		if !strings.EqualFold(filepath.Ext(abs), ".xp3") {
			return Game{}, fmt.Errorf("project is not a directory or XP3 file: %s", abs)
		}
		if !projectHasStartup(abs) {
			return Game{}, fmt.Errorf("startup.tjs not found in XP3 archive %q", abs)
		}
		return Game{ID: GameID(abs), Title: projectTitle(abs), Project: abs, Renderer: RendererGPU}, nil
	}
	if regularFile(filepath.Join(abs, "startup.tjs")) {
		return Game{ID: GameID(abs), Title: filepath.Base(abs), Project: abs, Renderer: RendererGPU}, nil
	}
	dataDir := filepath.Join(abs, "data")
	if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		if regularFile(filepath.Join(dataDir, "startup.tjs")) {
			return Game{ID: GameID(abs), Title: filepath.Base(abs), Project: abs, Renderer: RendererGPU}, nil
		}
		if entry := startupArchive(dataDir); entry != "" {
			return Game{ID: GameID(entry), Title: projectTitle(entry), Project: entry, Renderer: RendererGPU}, nil
		}
	}
	if entry := startupArchive(abs); entry != "" {
		return Game{ID: GameID(entry), Title: projectTitle(entry), Project: entry, Renderer: RendererGPU}, nil
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

func startupArchive(dir string) string {
	storage, err := assets.OpenStorage(dir, core.DefaultLimits())
	if err != nil {
		return ""
	}
	defer func() {
		for _, archive := range storage.Archives {
			_ = archive.Close()
		}
	}()
	if _, err := storage.Resolve("startup.tjs"); err != nil {
		return ""
	}
	for i := len(storage.Archives) - 1; i >= 0; i-- {
		if _, ok := storage.Archives[i].Entries["startup.tjs"]; ok {
			return storage.Archives[i].Path
		}
	}
	return ""
}

func projectTitle(project string) string {
	dir := filepath.Dir(project)
	if strings.EqualFold(filepath.Base(dir), "data") {
		dir = filepath.Dir(dir)
	}
	return filepath.Base(dir)
}

func projectDirectory(project string) string {
	info, err := os.Stat(project)
	if err == nil && !info.IsDir() {
		dir := filepath.Dir(project)
		if strings.EqualFold(filepath.Base(dir), "data") {
			return filepath.Dir(dir)
		}
		return dir
	}
	return project
}
