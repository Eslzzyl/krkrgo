package launcher

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/eslzzyl/krkrgo/internal/launcher/locales"
)

func (a *launcherApp) addGame(game Game) error {
	if game.SaveDir == "" {
		game.SaveDir = defaultSaveDir(a.catalogPath, game.ID, game.Title)
	}
	saved, err := a.catalog.Add(game)
	if err != nil {
		return err
	}
	a.selected = saved.ID
	a.syncFields()
	a.persist(true)
	a.notify(locales.AddedGame(saved.Title))
	return nil
}

func (a *launcherApp) chooseProject() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            a.window,
			Title:             locales.AddGameProject(),
			Filters:           []mygo.FileFilter{{Name: "XP3 archives", Extensions: []string{"xp3"}}},
			CreateDirectories: false,
		})
		if err != nil {
			a.post(func() { a.notify(locales.ChooseProjectFailed(err.Error())) })
			return
		}
		if len(paths) == 0 {
			return
		}
		game, err := InspectProject(paths[0])
		a.post(func() {
			if err != nil {
				a.notify(locales.UnsupportedProject(err.Error()))
				return
			}
			if err := a.addGame(game); err != nil {
				a.notify(locales.AddProjectFailed(err.Error()))
			}
		})
	}()
}

func (a *launcherApp) askRemove() {
	if a.selectedGame() != nil {
		a.confirmRemove = true
	}
}

func (a *launcherApp) removeSelected() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	index := -1
	for i := range a.catalog.Games {
		if a.catalog.Games[i].ID == game.ID {
			index = i
			break
		}
	}
	if run, ok := a.running[game.ID]; ok {
		run.process.Stop()
		game.PlayTimeSec += int64(time.Since(run.started).Seconds())
		game.LastPlayedAt = time.Now().Unix()
		delete(a.running, game.ID)
	}
	title := game.Title
	if !a.catalog.Remove(game.ID) {
		return
	}
	if len(a.catalog.Games) == 0 {
		a.selected = ""
	} else {
		if index >= len(a.catalog.Games) {
			index = len(a.catalog.Games) - 1
		}
		if index < 0 {
			index = 0
		}
		a.selected = a.catalog.Games[index].ID
	}
	a.syncFields()
	a.persist(true)
	a.notify(locales.RemovedGame(title))
}

func (a *launcherApp) relink(game Game) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.window,
			Title:   locales.LocateGame(game.Title),
			Filters: []mygo.FileFilter{{Name: "XP3 archives", Extensions: []string{"xp3"}}},
		})
		if err != nil {
			a.post(func() { a.notify(locales.ChooseProjectFailed(err.Error())) })
			return
		}
		if len(paths) == 0 {
			return
		}
		found, err := InspectProject(paths[0])
		a.post(func() {
			if err != nil {
				a.notify(locales.UnsupportedProject(err.Error()))
				return
			}
			for i := range a.catalog.Games {
				existing := &a.catalog.Games[i]
				if existing.ID != game.ID && existing.Project == found.Project {
					a.notify(locales.ProjectAlreadyListed())
					return
				}
			}
			target := a.catalog.Find(game.ID)
			if target == nil {
				return
			}
			target.Project = found.Project
			delete(a.availability, target.ID)
			a.persist(true)
			a.notify(locales.RelinkedGame(target.Title))
		})
	}()
}

func (a *launcherApp) chooseSaveDir(game Game) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:            a.window,
			Title:             locales.ChooseSaveDirectory(),
			Directory:         true,
			CreateDirectories: true,
		})
		if err != nil {
			a.post(func() { a.notify(locales.ChooseSaveDirFailed(err.Error())) })
			return
		}
		if len(paths) == 0 {
			return
		}
		a.post(func() {
			target := a.catalog.Find(game.ID)
			if target == nil {
				return
			}
			target.SaveDir = paths[0]
			a.persist(true)
			a.notify(locales.SaveDirectoryUpdated())
		})
	}()
}

func (a *launcherApp) chooseCover(game Game) {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:  a.window,
			Title:   locales.ChooseCoverImage(),
			Filters: []mygo.FileFilter{{Name: locales.Images(), Extensions: []string{"png", "jpg", "jpeg"}}},
		})
		if err != nil {
			a.post(func() { a.notify(locales.ChooseCoverFailed(err.Error())) })
			return
		}
		if len(paths) == 0 {
			return
		}
		a.post(func() {
			target := a.catalog.Find(game.ID)
			if target == nil {
				return
			}
			target.Cover = paths[0]
			a.persist(true)
			a.notify(locales.CoverUpdated())
		})
	}()
}

func (a *launcherApp) clearCover(game Game) {
	target := a.catalog.Find(game.ID)
	if target == nil {
		return
	}
	target.Cover = ""
	a.persist(true)
	a.notify(locales.CoverReset())
}

func (a *launcherApp) launchSelected() {
	a.launchGame(a.selectedGame())
}

func (a *launcherApp) launchGame(game *Game) {
	if game == nil {
		return
	}
	if _, ok := a.running[game.ID]; ok {
		a.stopGame(game.ID)
		return
	}
	if err := ValidateGame(*game); err != nil {
		a.notify(locales.LaunchFailed(err.Error()))
		return
	}
	if !ProjectAvailable(game.Project) {
		a.notify(locales.ProjectFilesMissing(game.Project))
		return
	}
	executable, err := os.Executable()
	if err != nil {
		a.notify(locales.ExecutableMissing(err.Error()))
		return
	}
	if a.logsDir == "" {
		a.notify(locales.LogsDirectoryMissing())
		return
	}
	process, err := Start(*game, executable, a.logPath(*game))
	if err != nil {
		a.notify(locales.LaunchFailed(err.Error()))
		return
	}
	game.LaunchCount++
	game.LastPlayedAt = time.Now().Unix()
	run := &activeRun{process: process, started: time.Now()}
	a.running[game.ID] = run
	a.persist(true)
	a.notify(locales.LaunchedGame(game.Title))

	id, title := game.ID, game.Title
	go func() {
		err := process.Wait()
		a.post(func() {
			run, ok := a.running[id]
			if !ok {
				return
			}
			delete(a.running, id)
			elapsed := int64(time.Since(run.started).Seconds())
			if game := a.catalog.Find(id); game != nil {
				game.PlayTimeSec += elapsed
				game.LastPlayedAt = time.Now().Unix()
			}
			a.persist(true)
			if err != nil {
				a.notify(locales.GameExitedError(title, err.Error()))
			} else {
				a.notify(locales.GameExited(title))
			}
		})
	}()
}

func (a *launcherApp) stopSelected() {
	a.stopGame(a.selected)
}

func (a *launcherApp) stopGame(id string) {
	run, ok := a.running[id]
	if !ok {
		return
	}
	run.process.Stop()
	if game := a.catalog.Find(id); game != nil {
		a.notify(locales.StoppingGame(game.Title))
	}
}

func (a *launcherApp) openProjectFolder() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	if err := mygo.Shell.OpenPath(projectDirectory(game.Project)); err != nil {
		a.notify(locales.OpenProjectFailed(err.Error()))
	}
}

func (a *launcherApp) openSaveDir() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	if game.SaveDir == "" {
		a.notify(locales.NoSaveDirectory())
		return
	}
	if err := os.MkdirAll(game.SaveDir, 0o755); err != nil {
		a.notify(locales.CreateSaveDirFailed(err.Error()))
		return
	}
	if err := mygo.Shell.OpenPath(game.SaveDir); err != nil {
		a.notify(locales.OpenSaveDirFailed(err.Error()))
	}
}

func (a *launcherApp) logPath(game Game) string {
	return filepath.Join(a.logsDir, safeName(game.ID)+".log")
}

func (a *launcherApp) openLog() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	path := a.logPath(*game)
	if _, err := os.Stat(path); err != nil {
		a.notify(locales.NoLogYet())
		return
	}
	if err := mygo.Shell.OpenPath(path); err != nil {
		a.notify(locales.OpenLogFailed(err.Error()))
	}
}

func (a *launcherApp) loadLog() {
	game := a.selectedGame()
	if game == nil {
		a.logGameID = ""
		a.logLines = nil
		return
	}
	a.logGameID = game.ID
	a.logLines = tailLines(a.logPath(*game), 200)
}

func (a *launcherApp) cover(game Game) *ui.Bitmap {
	path := game.Cover
	if path == "" {
		path = DiscoverCover(game.Project)
	}
	if path == "" {
		return nil
	}
	if bitmap, ok := a.covers[path]; ok {
		return bitmap
	}
	if a.coverFailed[path] {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		a.coverFailed[path] = true
		return nil
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		a.coverFailed[path] = true
		return nil
	}
	a.covers[path] = bitmap
	return bitmap
}

func (a *launcherApp) commitBudget() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	value, err := strconv.ParseUint(strings.TrimSpace(a.budgetText), 10, 64)
	if err != nil {
		a.status = locales.BudgetInvalid()
		return
	}
	game.Budget = value
	a.scheduleSave()
}

func (a *launcherApp) commitRenderer() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	if a.rendererChoice == 1 {
		game.Renderer = RendererCPU
	} else {
		game.Renderer = RendererGPU
	}
	a.scheduleSave()
}

func (a *launcherApp) commitRenderScale() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	scale := int(a.renderScale)
	if scale < 0 {
		scale = 0
	}
	if scale > 4 {
		scale = 4
	}
	game.RenderScale = scale
	a.scheduleSave()
}

func (a *launcherApp) saveTitle() {
	game := a.selectedGame()
	if game == nil {
		return
	}
	title := strings.TrimSpace(a.titleDraft)
	if title == "" {
		a.notify(locales.TitleEmpty())
		return
	}
	game.Title = title
	a.titleEditing = false
	a.persist(true)
	a.notify(locales.RenamedGame(title))
}

func tailLines(path string, maxLines int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		return nil
	}
	const limit = 64 * 1024
	start := info.Size() - limit
	if start < 0 {
		start = 0
	}
	buffer := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buffer, start); err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(buffer), "\r\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

func (a *launcherApp) formatPlayTime(seconds int64) string {
	if seconds <= 0 {
		return locales.NeverPlayed()
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	switch {
	case hours > 0:
		return locales.DurationHoursMinutes(int(hours), int(minutes))
	case minutes > 0:
		return locales.DurationMinutes(int(minutes))
	default:
		return locales.DurationSeconds(int(seconds))
	}
}

func (a *launcherApp) formatLastPlayed(unix int64) string {
	if unix == 0 {
		return locales.Never()
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}
