package launcher

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/eslzzyl/krkrgo/internal/launcher/locales"
)

type activeRun struct {
	process *Process
	started time.Time
}

type launcherApp struct {
	window      *mygo.Window
	catalogPath string
	logsDir     string
	catalog     Catalog

	selected string
	chosen   int
	query    string
	viewMode int
	tab      int

	running map[string]*activeRun

	availability map[string]bool
	homeDir      string

	covers      map[string]*ui.Bitmap
	coverFailed map[string]bool

	titleEditing bool
	titleDraft   string

	confirmRemove bool

	budgetText     string
	rendererChoice int
	renderScale    float64

	logGameID string
	logLines  []string

	status string
	toast  string

	list   ui.ListState
	grid   ui.GridState
	search ui.Handle

	saveTimer *time.Timer
}

func Run() {
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	mygo.App.SetName("krkrgo")
	app := &launcherApp{
		chosen:       -1,
		running:      map[string]*activeRun{},
		availability: map[string]bool{},
		covers:       map[string]*ui.Bitmap{},
		coverFailed:  map[string]bool{},
	}
	mygo.App.OnSecondInstance(func([]string, string) {
		if app.window != nil {
			app.window.Show()
			app.window.Focus()
		}
	})
	mygo.App.WhenReady(app.ready)
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (a *launcherApp) ready() {
	mygo.App.OnLocaleChanged(func(locale string) {
		a.setMenu()
		a.resetStatus()
	})
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.catalogPath = filepath.Join(dir, "catalog.json")
	}
	if dir, err := mygo.App.Path(mygo.PathLogs); err == nil {
		a.logsDir = dir
	}
	if dir, err := mygo.App.Path(mygo.PathHome); err == nil {
		a.homeDir = dir
	}
	if a.catalogPath != "" {
		catalog, err := LoadCatalog(a.catalogPath)
		if err != nil {
			a.status = locales.CatalogLoadFailed(err.Error())
			catalog = EmptyCatalog()
		}
		a.catalog = catalog
	} else {
		a.catalog = EmptyCatalog()
		a.status = locales.UserDataMissing()
	}
	if a.catalog.SortMode == "" {
		a.catalog.SortMode = SortManual
	}
	changed := false
	for i := range a.catalog.Games {
		if a.catalog.Games[i].SaveDir == "" {
			game := &a.catalog.Games[i]
			game.SaveDir = defaultSaveDir(a.catalogPath, game.ID, game.Title)
			changed = true
		}
	}
	a.selected = a.catalog.LastSelected
	if a.catalog.Find(a.selected) == nil && len(a.catalog.Games) > 0 {
		a.selected = a.catalog.Games[0].ID
	}
	a.resetStatus()
	a.syncFields()

	a.window = mygo.NewWindow(mygo.WindowOptions{
		Title:     "krkrgo",
		Width:     1120,
		Height:    760,
		MinWidth:  940,
		MinHeight: 620,
		StateKey:  "krkrgo-launcher",
		Content:   ui.View(a.view),
	})
	a.window.OnFileDrop(a.handleDrop)
	a.window.OnClose(func(*mygo.CloseEvent) { a.shutdown() })
	a.setMenu()
	if changed {
		a.persist(false)
	}
}

func (a *launcherApp) setMenu() {
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: locales.MenuFile(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuAddGame(), Accelerator: "CmdOrCtrl+O", Click: func(*mygo.MenuItem, *mygo.Window) { a.chooseProject() }},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: locales.MenuGame(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuLaunch(), Accelerator: "CmdOrCtrl+Return", Click: func(*mygo.MenuItem, *mygo.Window) { a.launchSelected() }},
			{Label: locales.MenuStop(), Accelerator: "CmdOrCtrl+.", Click: func(*mygo.MenuItem, *mygo.Window) { a.stopSelected() }},
			mygo.Separator(),
			{Label: locales.MenuRevealProject(), Click: func(*mygo.MenuItem, *mygo.Window) { a.revealProject() }},
			{Label: locales.MenuRemove(), Click: func(*mygo.MenuItem, *mygo.Window) { a.confirmRemove = true }},
		}},
		{Label: locales.MenuView(), Submenu: []*mygo.MenuItem{
			{Label: locales.MenuList(), Type: mygo.MenuItemRadio, Checked: a.viewMode == 0, Click: func(*mygo.MenuItem, *mygo.Window) { a.viewMode = 0 }},
			{Label: locales.MenuGrid(), Type: mygo.MenuItemRadio, Click: func(*mygo.MenuItem, *mygo.Window) { a.viewMode = 1 }},
		}},
		{Role: mygo.RoleWindowMenu},
	}))
}

func (a *launcherApp) shutdown() {
	for id, run := range a.running {
		run.process.Stop()
		if game := a.catalog.Find(id); game != nil {
			game.PlayTimeSec += int64(time.Since(run.started).Seconds())
			game.LastPlayedAt = time.Now().Unix()
		}
		delete(a.running, id)
	}
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.persist(false)
}

func (a *launcherApp) resetStatus() {
	if len(a.catalog.Games) > 0 {
		a.status = locales.GamesReady(len(a.catalog.Games))
		return
	}
	a.status = locales.ChooseGamePrompt()
}

func (a *launcherApp) notify(message string) {
	a.status = message
	a.toast = message
}

func (a *launcherApp) selectedGame() *Game {
	return a.catalog.Find(a.selected)
}

func (a *launcherApp) syncFields() {
	game := a.selectedGame()
	if game == nil {
		a.budgetText = "0"
		a.rendererChoice = 0
		a.renderScale = 0
		a.titleEditing = false
		a.logGameID = ""
		a.logLines = nil
		return
	}
	a.budgetText = strconv.FormatUint(game.Budget, 10)
	if game.Renderer == RendererCPU {
		a.rendererChoice = 1
	} else {
		a.rendererChoice = 0
	}
	a.renderScale = float64(game.RenderScale)
	a.titleEditing = false
	a.logGameID = ""
	a.logLines = nil
}

func (a *launcherApp) persist(report bool) bool {
	if a.catalogPath == "" {
		if report {
			a.notify(locales.CatalogSaveUnavailable())
		}
		return false
	}
	a.catalog.LastSelected = a.selected
	if err := SaveCatalog(a.catalogPath, a.catalog); err != nil {
		if report {
			a.notify(locales.CatalogSaveFailed(err.Error()))
		}
		return false
	}
	return true
}

func (a *launcherApp) scheduleSave() {
	if a.saveTimer != nil {
		a.saveTimer.Stop()
	}
	a.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
		if a.window == nil {
			return
		}
		a.post(func() { a.persist(true) })
	})
}

// post runs fn on the main thread, where the window and the launcher
// state live, after the current event.
func (a *launcherApp) post(fn func()) {
	a.window.Update(fn)
}

func (a *launcherApp) handleDrop(e *mygo.FileDropEvent) {
	paths := append([]string(nil), e.Paths...)
	if len(paths) == 0 {
		return
	}
	a.status = locales.AddingDroppedProject()
	go func() {
		var found []Game
		var failures []string
		for _, path := range paths {
			info, err := os.Stat(path)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			if !info.IsDir() && !strings.EqualFold(filepath.Ext(path), ".xp3") {
				failures = append(failures, path+" is not a project directory or XP3 archive")
				continue
			}
			game, err := InspectProject(path)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			found = append(found, game)
		}
		a.post(func() {
			added := 0
			for _, game := range found {
				if err := a.addGame(game); err == nil {
					added++
				} else {
					failures = append(failures, err.Error())
				}
			}
			switch {
			case added > 0 && len(failures) == 0:
				a.notify(locales.GamesAdded(added))
			case added > 0:
				a.notify(locales.GamesAddedWithFailures(strings.Join(failures, " "), added))
			case len(failures) > 0:
				a.notify(locales.AddProjectFailed(failures[0]))
			}
		})
	}()
}

func defaultSaveDir(catalogFile, id, title string) string {
	if id == "" {
		id = safeName(title)
	}
	return filepath.Join(filepath.Dir(catalogFile), "saves", id)
}

func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "game"
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 {
			return '_'
		}
		return r
	}, name)
}
