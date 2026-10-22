// Package krkrgo exposes the Go host used by the Android launcher.
package krkrgo

import (
	"fmt"
	"strings"
	"sync"

	"github.com/eslzzyl/krkrgo/internal/host"
	"github.com/eslzzyl/krkrgo/internal/krkrgocli"
	"github.com/hajimehoshi/ebiten/v2"
)

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
