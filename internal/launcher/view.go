package launcher

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/eslzzyl/krkrgo/internal/launcher/locales"
)

func (a *launcherApp) view(c *ui.Context) {
	a.applyTheme(c)
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	a.shortcuts(c)
	if a.tab == 2 && a.logGameID != a.selected {
		a.loadLog()
	}

	order := VisibleGames(a.catalog.Games, a.query, a.catalog.SortMode)
	a.syncChosen(order)

	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Fill().Grow(1).Children(func() {
			a.sidebar(c, order)
			a.detail(c)
		})
		a.statusBar(c, order)
	})
	a.commitChosen(order)

	if a.confirmRemove {
		game := a.selectedGame()
		message := locales.RemoveGameMessage()
		if game != nil {
			message = locales.RemoveGameMessageTitle(game.Title)
		}
		if ui.AlertDialog(c, &a.confirmRemove, locales.RemoveGameTitle(), message, locales.Cancel(), locales.Remove()) == 1 {
			a.removeSelected()
		}
	}
}

func (a *launcherApp) statusBar(c *ui.Context, order []int) {
	t := c.Theme()
	ui.Row(c).Height(34).Padding(0, 18).Background(sidebarColor(t)).BorderStyle(ui.BorderSolid).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.status).TextColor(t.TextMuted).FontSize(12).SingleLine().Grow(1)
		if len(a.running) > 0 {
			ui.Text(c, locales.Running()).FontSize(11).TextColor(t.Success)
		}
	})
}

func (a *launcherApp) shortcuts(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyF, func() { a.search.Focus() })
	c.OnShortcut(ui.Cmd, ui.KeyO, func() { a.chooseProject() })
	c.OnShortcut(ui.Cmd, ui.KeyBackspace, func() { a.askRemove() })
	if a.titleEditing && c.Shortcut(0, ui.KeyEscape) {
		a.titleEditing = false
		return
	}
	if a.query != "" && c.Shortcut(0, ui.KeyEscape) {
		a.query = ""
	}
}

func (a *launcherApp) syncChosen(order []int) {
	if a.chosen >= 0 && a.chosen < len(order) && a.catalog.Games[order[a.chosen]].ID == a.selected {
		return
	}
	a.chosen = -1
	for i, index := range order {
		if a.catalog.Games[index].ID == a.selected {
			a.chosen = i
			return
		}
	}
}

func (a *launcherApp) commitChosen(order []int) {
	if a.chosen < 0 || a.chosen >= len(order) {
		return
	}
	id := a.catalog.Games[order[a.chosen]].ID
	if id == a.selected {
		return
	}
	a.selected = id
	a.syncFields()
	a.scheduleSave()
}

func (a *launcherApp) sidebar(c *ui.Context, order []int) {
	t := c.Theme()
	ui.Column(c).Width(360).FillHeight().Background(sidebarColor(t)).Padding(16).Gap(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "krkrgo").FontSize(18).Bold()
			ui.Spacer(c)
			if count := len(a.catalog.Games); count > 0 {
				ui.Badge(c, fmt.Sprintf("%d", count)).Background(t.Accent.Alpha(0.12)).TextColor(t.Accent)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c, &a.query).Bind(&a.search).Label(locales.SearchGames()).Grow(1)
			add := a.iconAction(c, iconAdd, locales.Add(), locales.AddGameTooltip())
			if add.Clicked() {
				a.chooseProject()
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			a.sortMenu(c)
			ui.Segmented(c, &a.viewMode, locales.MenuList(), locales.MenuGrid()).Changed()
			ui.Spacer(c)
			refresh := a.iconAction(c, iconRefresh, locales.RecheckProjects(), "")
			if refresh.Clicked() {
				a.availability = map[string]bool{}
				a.notify(locales.ProjectsRechecked())
			}
		})
		if a.viewMode == 1 {
			a.gridView(c, order)
		} else {
			a.listView(c, order)
		}
	})
}

func (a *launcherApp) sortMenu(c *ui.Context) {
	options := []struct{ label, mode string }{
		{locales.SortManual(), SortManual},
		{locales.SortTitle(), SortTitle},
		{locales.SortRecent(), SortRecent},
		{locales.SortAdded(), SortAdded},
	}
	ui.MenuButton(c, locales.Sort(), func(m *ui.Menu) {
		for _, option := range options {
			item := m.Item(option.label).Checked(a.catalog.SortMode == option.mode)
			if item.Chosen() {
				a.catalog.SortMode = option.mode
				a.persist(true)
				a.status = locales.SortedBy(option.label)
			}
		}
	})
}

func (a *launcherApp) listView(c *ui.Context, order []int) {
	t := c.Theme()
	a.list.Selected = &a.chosen
	a.list.Key = func(i int) any { return a.catalog.Games[order[i]].ID }
	a.list.Label = func(i int) string { return a.catalog.Games[order[i]].Title }
	if a.query == "" && a.catalog.SortMode == SortManual {
		a.list.Reorder = func(rows []int, to int) {
			if len(rows) != 1 {
				return
			}
			a.catalog.Move(rows[0], to)
			a.persist(true)
			a.status = locales.OrderUpdated()
		}
	} else {
		a.list.Reorder = nil
	}
	list := ui.List(c, &a.list, len(order), func(i int) {
		a.gameRow(c, order, i)
	}).Grow(1).Children(func() {
		if len(order) == 0 {
			message := locales.NoGames()
			if a.query != "" {
				message = locales.NoGamesMatch(a.query)
			}
			ui.Text(c, message).TextColor(t.TextMuted).Padding(12)
		}
	})
	a.commitChosen(order)
	if list.Submitted() {
		a.launchSelected()
	}
}

func (a *launcherApp) gameRow(c *ui.Context, order []int, i int) {
	game := &a.catalog.Games[order[i]]
	t := c.Theme()
	selected := i == a.chosen
	textColor := t.Text
	mutedColor := t.TextMuted
	starColor := t.TextMuted
	if selected {
		textColor = t.AccentText
		mutedColor = t.AccentText.Alpha(0.8)
		starColor = t.AccentText
	}
	row := ui.Row(c).Padding(8, 10).Gap(10).AlignItems(ui.Center).Radius(10)
	if !selected && row.Hovered() {
		row.Background(t.SurfaceHover)
	}
	row.Children(func() {
		if bitmap := a.cover(*game); bitmap != nil {
			ui.Image(c, bitmap).Size(40, 54).Radius(6)
		} else {
			tint := titleTint(game.Title)
			background := tint.Alpha(0.18)
			foreground := tint
			if selected {
				background = t.AccentText.Alpha(0.18)
				foreground = t.AccentText
			}
			ui.Column(c).Size(40, 54).Radius(6).Background(background).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, initial(game.Title)).FontSize(16).Bold().TextColor(foreground)
			})
		}
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			title := game.Title
			if a.running[game.ID] != nil {
				title += "  ●"
			}
			ui.Text(c, title).FontSize(14).TextColor(textColor).SingleLine()
			ui.Text(c, a.shortPath(game.Project)).FontSize(11).TextColor(mutedColor).SingleLine()
			ui.Row(c).Gap(6).Children(func() {
				badge := func(text string, color ui.Color) {
					background, foreground := color.Alpha(0.14), color
					if selected {
						background, foreground = t.AccentText.Alpha(0.18), t.AccentText
					}
					ui.Badge(c, text).Background(background).TextColor(foreground)
				}
				if !a.isAvailable(*game) {
					badge(locales.Missing(), t.Danger)
				}
				if game.Favorite && !selected {
					badge(locales.Favorite(), t.Warning)
				}
				if a.running[game.ID] != nil {
					badge(locales.Running(), t.Success)
				}
			})
		})
		star := "☆"
		if game.Favorite {
			star = "★"
		}
		if !selected && game.Favorite {
			starColor = t.Warning
		}
		favorite := ui.ButtonBase(c.Key("favorite-"+game.ID)).Size(26, 26).Label(locales.Favorite()).Radius(6).Tooltip(locales.Favorite())
		if favorite.Hovered() {
			if selected {
				favorite.Background(t.AccentText.Alpha(0.16))
			} else {
				favorite.Background(t.SurfaceHover)
			}
		}
		favorite.Children(func() {
			ui.Text(c, star).FontSize(15).TextColor(starColor)
		})
		if favorite.Clicked() {
			a.toggleFavorite(game)
		}
	})
}

func (a *launcherApp) gridView(c *ui.Context, order []int) {
	a.grid.Selected = &a.chosen
	a.grid.Key = func(i int) any { return a.catalog.Games[order[i]].ID }
	a.grid.Label = func(i int) string { return a.catalog.Games[order[i]].Title }
	if a.query == "" && a.catalog.SortMode == SortManual {
		a.grid.Reorder = func(items []int, to int) {
			if len(items) != 1 {
				return
			}
			a.catalog.Move(items[0], to)
			a.persist(true)
			a.status = locales.OrderUpdated()
		}
	} else {
		a.grid.Reorder = nil
	}
	grid := ui.GridView(c, &a.grid, len(order), 160, 224, func(i int) {
		a.gridCell(c, order, i)
	}).Grow(1)
	a.commitChosen(order)
	if grid.Submitted() {
		a.launchSelected()
	}
}

func (a *launcherApp) gridCell(c *ui.Context, order []int, i int) {
	game := &a.catalog.Games[order[i]]
	t := c.Theme()
	selected := i == a.chosen
	cell := ui.Column(c).Fill().Padding(10).Gap(8).Radius(14).Background(t.Surface).Border(1, t.Border).AlignItems(ui.Center)
	if selected {
		cell.Border(2, t.Accent)
	} else if cell.Hovered() {
		cell.Background(t.SurfaceHover)
	}
	cell.Children(func() {
		if bitmap := a.cover(*game); bitmap != nil {
			ui.Image(c, bitmap).Size(132, 168).Radius(8)
		} else {
			tint := titleTint(game.Title)
			ui.Column(c).Size(132, 168).Radius(8).Background(tint.Alpha(0.16)).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
				ui.Text(c, initial(game.Title)).FontSize(34).Bold().TextColor(tint)
			})
		}
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, game.Title).FontSize(13).TextColor(t.Text).SingleLine()
			if a.running[game.ID] != nil {
				ui.Text(c, locales.Running()).FontSize(10).TextColor(t.Success)
			} else if !a.isAvailable(*game) {
				ui.Text(c, locales.Missing()).FontSize(10).TextColor(t.Danger)
			} else if game.Favorite {
				ui.Text(c, "★").FontSize(10).TextColor(t.Warning)
			}
		})
	})
}

func (a *launcherApp) detail(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).FillHeight().Padding(24).Gap(18).Children(func() {
		game := a.selectedGame()
		if game == nil {
			ui.Column(c).Fill().Justify(ui.Center).AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Icon(c, iconGamepad).FontSize(56).TextColor(t.TextMuted)
				ui.Text(c, locales.NoGameSelected()).FontSize(24).Bold()
				ui.Text(c, locales.EmptyHint()).FontSize(13).TextColor(t.TextMuted).Width(460)
				if ui.PrimaryButton(c, locales.AddGame()).Clicked() {
					a.chooseProject()
				}
			})
			return
		}

		ui.Row(c).Gap(24).Children(func() {
			a.detailCover(c, *game)
			ui.Column(c).Grow(1).Gap(10).Children(func() {
				if a.titleEditing {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.TextInput(c, &a.titleDraft).Grow(1).Label(locales.GameTitle())
						if ui.PrimaryButton(c, locales.Save()).Clicked() {
							a.saveTitle()
						}
						if ui.Button(c, locales.Cancel()).Clicked() {
							a.titleEditing = false
						}
					})
				} else {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, game.Title).FontSize(28).Bold().SingleLine().Grow(1)
						rename := ui.Button(c, "").Size(30, 30).Label(locales.Rename()).Tooltip(locales.Rename()).Children(func() {
							ui.Icon(c, iconPencil).FontSize(14)
						})
						if rename.Clicked() {
							a.titleDraft = game.Title
							a.titleEditing = true
						}
					})
				}
				ui.Text(c, game.Project).FontSize(12).TextColor(t.TextMuted).SingleLine()
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if a.isAvailable(*game) {
						ui.Badge(c, locales.Ready()).Background(t.Success.Alpha(0.14)).TextColor(t.Success)
					} else {
						ui.Badge(c, locales.ProjectMissing()).Background(t.Danger.Alpha(0.14)).TextColor(t.Danger)
					}
					if game.Favorite {
						ui.Badge(c, locales.Favorite()).Background(t.Warning.Alpha(0.14)).TextColor(t.Warning)
					}
					if a.running[game.ID] != nil {
						ui.Badge(c, locales.Running()).Background(t.Success.Alpha(0.14)).TextColor(t.Success)
					}
				})
				ui.Text(c, locales.PlaytimeSummary(
					a.formatPlayTime(game.PlayTimeSec), a.formatLastPlayed(game.LastPlayedAt), locales.LaunchesCount(game.LaunchCount))).
					FontSize(12).TextColor(t.TextMuted)
				ui.Spacer(c)
				ui.Row(c).Gap(8).Wrap().Children(func() {
					label := locales.LaunchGame()
					if a.running[game.ID] != nil {
						label = locales.StopGame()
					}
					if ui.PrimaryButton(c, label).Clicked() {
						a.launchGame(game)
					}
					if button := a.iconButton(c, locales.OpenFolder(), iconFolder); button.Clicked() {
						a.revealProject()
					}
					if button := a.iconButton(c, locales.SaveFolder(), iconArchive); button.Clicked() {
						a.openSaveDir()
					}
					ui.MenuButton(c, locales.More(), func(m *ui.Menu) {
						if m.Item(locales.ShowLog()).Chosen() {
							a.openLog()
						}
						if m.Item(locales.RelinkProject()).Chosen() {
							a.relink(*game)
						}
						m.Separator()
						if m.Item(locales.MenuRemove()).Chosen() {
							a.confirmRemove = true
						}
					})
				})
			})
		})
		ui.Divider(c)
		a.detailTabs(c, game)
	})
}

func (a *launcherApp) iconAction(c *ui.Context, icon *ui.SVG, label, tooltip string) ui.Element {
	t := c.Theme()
	button := ui.ButtonBase(c).Size(30, 30).Label(label).Radius(6)
	if button.Hovered() {
		button.Background(t.SurfaceHover)
	}
	if tooltip != "" {
		button.Tooltip(tooltip)
	}
	button.Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(t.TextMuted)
	})
	return button
}

func (a *launcherApp) iconButton(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	return ui.Button(c, "").Label(label).Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).FontSize(14)
			ui.Text(c, label).FontSize(12)
		})
	})
}

func (a *launcherApp) detailCover(c *ui.Context, game Game) {
	t := c.Theme()
	if bitmap := a.cover(game); bitmap != nil {
		ui.Image(c, bitmap).Size(200, 267).Radius(14).Shadow(0, 12, 30, 0, cardShadow(t))
		return
	}
	tint := titleTint(game.Title)
	ui.Column(c).Size(200, 267).Radius(14).Background(tint.Alpha(0.14)).Border(1, t.Border).Justify(ui.Center).AlignItems(ui.Center).Gap(10).Children(func() {
		ui.Text(c, initial(game.Title)).FontSize(48).Bold().TextColor(tint)
		ui.Text(c, locales.NoCover()).FontSize(11).TextColor(t.TextMuted)
	})
}

func (a *launcherApp) detailTabs(c *ui.Context, game *Game) {
	if ui.Tabs(c, &a.tab, locales.TabOverview(), locales.TabSettings(), locales.TabLog()).Changed() {
		if a.tab == 2 {
			a.loadLog()
		}
	}
	if a.tab == 2 {
		a.logPanel(c)
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		switch a.tab {
		case 1:
			a.settings(c, game)
		default:
			a.overview(c, game)
		}
	})
}

func (a *launcherApp) overview(c *ui.Context, game *Game) {
	renderer := "GPU"
	if game.Renderer == RendererCPU {
		renderer = "CPU"
	}
	scale := locales.FollowDisplay()
	if game.RenderScale > 0 {
		scale = fmt.Sprintf("%dx", game.RenderScale)
	}
	budget := locales.Unlimited()
	if game.Budget > 0 {
		budget = locales.Instructions(strconv.FormatUint(game.Budget, 10))
	}
	rows := [][2]string{
		{locales.Project(), game.Project},
		{locales.SaveDirectory(), game.SaveDir},
		{locales.Renderer(), renderer},
		{locales.RenderScale(), scale},
		{locales.InstructionBudget(), budget},
		{locales.Launches(), fmt.Sprintf("%d", game.LaunchCount)},
		{locales.Playtime(), a.formatPlayTime(game.PlayTimeSec)},
		{locales.LastPlayed(), a.formatLastPlayed(game.LastPlayedAt)},
	}
	ui.Column(c).Gap(8).Children(func() {
		a.sectionCard(c, locales.Details(), func() {
			for _, row := range rows {
				a.detailField(c, row[0], row[1])
			}
			if !a.isAvailable(*game) {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.ProjectMissingHint()).FontSize(12).TextColor(c.Theme().Danger)
					if button := a.iconButton(c, locales.Relink(), iconFolder); button.Clicked() {
						a.relink(*game)
					}
				})
			}
		})
	})
}

func (a *launcherApp) settings(c *ui.Context, game *Game) {
	t := c.Theme()
	label := func(text string) {
		ui.Text(c, text).FontSize(11).TextColor(t.TextMuted)
	}
	ui.Column(c).Gap(14).Children(func() {
		a.sectionCard(c, locales.Storage(), func() {
			label(locales.SaveDirectory())
			ui.Row(c).Gap(8).Children(func() {
				if ui.TextInput(c, &game.SaveDir).AlignSelf(ui.Stretch).Grow(1).Changed() {
					a.scheduleSave()
				}
				if ui.Button(c, locales.Choose()).Clicked() {
					a.chooseSaveDir(*game)
				}
			})
		})
		a.sectionCard(c, locales.LaunchOptions(), func() {
			label(locales.BudgetHint())
			if ui.TextInput(c, &a.budgetText).AlignSelf(ui.Stretch).Changed() {
				a.commitBudget()
			}
			label(locales.Renderer())
			if ui.Segmented(c, &a.rendererChoice, "GPU", "CPU").Changed() {
				a.commitRenderer()
			}
			label(locales.RenderScaleHint())
			if ui.Stepper(c, &a.renderScale, 0, 4, 1).Changed() {
				a.commitRenderScale()
			}
		})
		a.sectionCard(c, locales.Cover(), func() {
			ui.Row(c).Gap(8).Children(func() {
				if button := a.iconButton(c, locales.ChooseImage(), iconImage); button.Clicked() {
					a.chooseCover(*game)
				}
				if ui.Button(c, locales.ResetAutomatic()).Clicked() {
					a.clearCover(*game)
				}
			})
		})
		a.sectionCard(c, locales.Project(), func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, game.Project).FontSize(12).SingleLine().Grow(1)
				if button := a.iconButton(c, locales.Relink(), iconFolder); button.Clicked() {
					a.relink(*game)
				}
			})
		})
	})
}

func (a *launcherApp) logPanel(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Grow(1).Gap(10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, iconLog).FontSize(15).TextColor(t.TextMuted)
			ui.Text(c, locales.GameLog()).FontSize(14).Bold()
			ui.Spacer(c)
			if button := a.iconButton(c, locales.Refresh(), iconRefresh); button.Clicked() {
				a.loadLog()
			}
			if button := a.iconButton(c, locales.Open(), iconFolder); button.Clicked() {
				a.openLog()
			}
		})
		ui.Scroll(c).Grow(1).Background(codeBackground(t)).Radius(12).Padding(14).Border(1, t.Border).Children(func() {
			if len(a.logLines) == 0 {
				ui.Text(c, locales.NoLogEntries()).TextColor(codeText(t).Alpha(0.7)).FontSize(12)
				return
			}
			ui.Text(c, strings.Join(a.logLines, "\n")).FontSize(12).TextColor(codeText(t))
		})
	})
}

func (a *launcherApp) sectionCard(c *ui.Context, title string, build func()) {
	t := c.Theme()
	ui.Column(c).FillWidth().Gap(10).Padding(16).Radius(14).Background(t.Surface).Border(1, t.Border).Children(func() {
		ui.Text(c, title).FontSize(11).Bold().TextColor(t.TextMuted)
		build()
	})
}

func (a *launcherApp) detailField(c *ui.Context, label, value string) {
	t := c.Theme()
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, label).Width(120).FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, value).Grow(1).FontSize(12).SingleLine()
	})
}

func (a *launcherApp) isAvailable(game Game) bool {
	if available, ok := a.availability[game.ID]; ok {
		return available
	}
	available := ProjectAvailable(game.Project)
	a.availability[game.ID] = available
	return available
}

func initial(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "?"
	}
	for _, r := range title {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func (a *launcherApp) shortPath(path string) string {
	if a.homeDir != "" && strings.HasPrefix(path, a.homeDir) {
		return "~" + strings.TrimPrefix(path, a.homeDir)
	}
	return path
}
