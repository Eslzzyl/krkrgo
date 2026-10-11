package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/eslzzyl/krkrgo/internal/krkrgocli"
	"github.com/eslzzyl/krkrgo/internal/launcher"
)

type launcherApp struct {
	window      *mygo.Window
	catalogPath string
	catalog     launcher.Catalog
	selected    int
	running     bool
	advanced    bool
	status      string
	budgetText  string
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		krkrgocli.RunGame(os.Args[2:])
		return
	}

	mygo.App.SetName("krkrgo")
	app := &launcherApp{selected: -1, status: "Choose a game project to begin."}
	mygo.App.WhenReady(func() {
		var err error
		app.catalogPath, err = catalogPath()
		if err == nil {
			app.catalog, err = launcher.LoadCatalog(app.catalogPath)
		}
		if err != nil {
			app.status = "Could not load game catalog: " + err.Error()
			app.catalog = launcher.EmptyCatalog()
		}
		if err == nil {
			changed := false
			for i := range app.catalog.Games {
				if app.catalog.Games[i].SaveDir == "" {
					app.catalog.Games[i].SaveDir = defaultSaveDir(app.catalogPath, app.catalog.Games[i].ID, app.catalog.Games[i].Title)
					changed = true
				}
			}
			if changed {
				_ = app.saveCatalog()
			}
			if len(app.catalog.Games) > 0 {
				app.selected = 0
				app.budgetText = strconv.FormatUint(app.catalog.Games[0].Budget, 10)
			}
		}
		app.window = mygo.NewWindow(mygo.WindowOptions{
			Title: "krkrgo", Width: 980, Height: 660, MinWidth: 680, MinHeight: 460,
			Content: ui.View(app.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func catalogPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "catalog.json"), nil
}

func defaultSaveDir(catalogFile, id, title string) string {
	if id == "" {
		id = safeName(title)
	}
	return filepath.Join(filepath.Dir(catalogFile), "saves", id)
}

func (a *launcherApp) view(c *ui.Context) {
	theme := c.Theme()
	ui.Column(c).Fill().Padding(18).Gap(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "krkrgo").FontSize(24).Bold()
			ui.Spacer(c)
			if ui.Button(c, "Add game").Clicked() {
				a.chooseProject()
			}
		})
		ui.Divider(c)
		ui.Row(c).Grow(1).Gap(16).Children(func() {
			ui.Scroll(c).Width(280).Background(theme.Surface).Padding(8).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					for i := range a.catalog.Games {
						index := i
						game := &a.catalog.Games[index]
						label := game.Title
						if a.running && index == a.selected {
							label += " (Running)"
						}
						button := ui.Button(c.Key(fmt.Sprintf("game-%d", index)), label).AlignSelf(ui.Stretch)
						if index == a.selected {
							button.Background(theme.Accent).TextColor(theme.AccentText)
						}
						if button.Clicked() {
							a.selected = index
							a.budgetText = strconv.FormatUint(game.Budget, 10)
						}
					}
				})
			})
			ui.Column(c).Grow(1).Gap(10).Padding(8).Children(func() {
				if game := a.selectedGame(); game != nil {
					ui.Text(c, game.Title).FontSize(22).Bold()
					ui.Text(c, game.Project).TextColor(theme.TextMuted)
					ui.Text(c, "Project status: Registered").Padding(0, 0, 8, 0)
					if a.advanced {
						ui.Divider(c)
						ui.Text(c, "Advanced launch settings").Bold()
						ui.Text(c, "Save directory").TextColor(theme.TextMuted)
						ui.TextInput(c, &game.SaveDir).AlignSelf(ui.Stretch)
						ui.Text(c, "Instruction budget (0 means unlimited)").TextColor(theme.TextMuted)
						ui.TextInput(c, &a.budgetText).AlignSelf(ui.Stretch)
					}
					ui.Spacer(c)
					ui.Row(c).Gap(8).Children(func() {
						if ui.PrimaryButton(c, launchLabel(a.running)).Clicked() {
							a.launch()
						}
						if ui.Button(c, advancedLabel(a.advanced)).Clicked() {
							a.advanced = !a.advanced
						}
						if ui.Button(c, "Remove").Clicked() && !a.running {
							a.removeSelected()
						}
					})
				} else {
					ui.Text(c, "No games added").FontSize(20).Bold()
					ui.Text(c, "Add a folder containing startup.tjs, either directly, under data/, or in an XP3 archive.").TextColor(theme.TextMuted)
				}
			})
		})
		ui.Divider(c)
		ui.Text(c, a.status).TextColor(theme.TextMuted)
	})
}

func launchLabel(running bool) string {
	if running {
		return "Game running"
	}
	return "Launch game"
}

func advancedLabel(open bool) string {
	if open {
		return "Hide settings"
	}
	return "Advanced settings"
}

func (a *launcherApp) selectedGame() *launcher.Game {
	if a.selected < 0 || a.selected >= len(a.catalog.Games) {
		return nil
	}
	return &a.catalog.Games[a.selected]
}

func (a *launcherApp) chooseProject() {
	window := a.window
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: window, Title: "Add game project", Directory: true, CreateDirectories: false})
		window.Update(func() {
			if err != nil {
				a.status = "Could not choose project: " + err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			game, err := launcher.InspectProject(paths[0])
			if err != nil {
				a.status = "Not a supported game project: " + err.Error()
				return
			}
			for _, existing := range a.catalog.Games {
				if existing.Project == game.Project {
					a.status = "That project is already in the catalog."
					return
				}
			}
			game.SaveDir = defaultSaveDir(a.catalogPath, game.ID, game.Title)
			a.catalog.Games = append(a.catalog.Games, game)
			a.selected = len(a.catalog.Games) - 1
			a.budgetText = "0"
			if a.saveCatalog() {
				a.status = "Added " + game.Title
			}
		})
	}()
}

func (a *launcherApp) saveCatalog() bool {
	if a.catalogPath == "" {
		a.status = "Could not save catalog: user data path is unavailable."
		return false
	}
	if err := launcher.SaveCatalog(a.catalogPath, a.catalog); err != nil {
		a.status = "Could not save catalog: " + err.Error()
		return false
	}
	return true
}

func (a *launcherApp) removeSelected() {
	if a.selectedGame() == nil {
		return
	}
	a.catalog.Games = append(a.catalog.Games[:a.selected], a.catalog.Games[a.selected+1:]...)
	if len(a.catalog.Games) == 0 {
		a.selected = -1
	} else if a.selected >= len(a.catalog.Games) {
		a.selected = len(a.catalog.Games) - 1
	}
	if a.saveCatalog() {
		a.status = "Game removed."
	}
}

func (a *launcherApp) launch() {
	game := a.selectedGame()
	if game == nil || a.running {
		return
	}
	budget, err := strconv.ParseUint(strings.TrimSpace(a.budgetText), 10, 64)
	if err != nil {
		a.status = "Invalid instruction budget: enter a whole number."
		return
	}
	game.Budget = budget
	if !a.saveCatalog() {
		return
	}
	logs, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		a.status = "Could not locate log directory: " + err.Error()
		return
	}
	runtimePath, err := os.Executable()
	if err != nil {
		a.status = "Could not locate krkrgo executable: " + err.Error()
		return
	}
	logPath := filepath.Join(logs, safeName(game.ID)+".log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		a.status = "Could not open game log: " + err.Error()
		return
	}
	args := []string{"run", "-project", game.Project}
	if budget != 0 {
		args = append(args, "-budget", strconv.FormatUint(budget, 10))
	}
	if game.SaveDir != "" {
		args = append(args, "-save-dir", game.SaveDir)
	}
	cmd := exec.Command(runtimePath, args...)
	cmd.Dir = game.Project
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		a.status = "Could not launch game: " + err.Error()
		return
	}
	a.running = true
	a.status = "Launched " + game.Title + " — log: " + logPath
	window := a.window
	go func() {
		waitErr := cmd.Wait()
		closeErr := logFile.Close()
		window.Update(func() {
			a.running = false
			if waitErr != nil {
				a.status = fmt.Sprintf("%s exited with an error: %v", game.Title, waitErr)
			} else if closeErr != nil {
				a.status = fmt.Sprintf("%s exited; log close failed: %v", game.Title, closeErr)
			} else {
				a.status = fmt.Sprintf("%s exited normally at %s", game.Title, time.Now().Format(time.Kitchen))
			}
		})
	}()
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
