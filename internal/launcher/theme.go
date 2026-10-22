package launcher

import "github.com/egoist/mygo/ui"

// applyTheme replaces the system theme with the launcher's palette for
// the current frame, keeping the system's light or dark appearance.
func (a *launcherApp) applyTheme(c *ui.Context) {
	base := c.Theme()
	t := *base
	t.Radius = 9
	t.Spacing = 4
	t.ScrollbarWidth = 8
	if t.Dark {
		t.Background = ui.Hex("#121216")
		t.Surface = ui.Hex("#1B1B21")
		t.SurfaceHover = ui.Hex("#24242C")
		t.SurfacePressed = ui.Hex("#2E2E38")
		t.Border = ui.Hex("#2E2E38")
		t.Text = ui.Hex("#F4F4F6")
		t.TextMuted = ui.Hex("#9B9BA6")
		t.Accent = ui.Hex("#60A5FA")
		t.AccentHover = ui.Hex("#93C5FD")
		t.AccentPressed = ui.Hex("#3B82F6")
		t.AccentText = ui.Hex("#0F172A")
		t.Danger = ui.Hex("#FF6B6B")
		t.Warning = ui.Hex("#F5B84B")
		t.Success = ui.Hex("#4ADE80")
		t.Selection = ui.RGBA(96, 165, 250, 0.3)
		t.Focus = ui.RGBA(96, 165, 250, 0.55)
		t.Scrollbar = ui.RGBA(255, 255, 255, 0.28)
	} else {
		t.Background = ui.Hex("#F5F5F7")
		t.Surface = ui.Hex("#FFFFFF")
		t.SurfaceHover = ui.Hex("#F0F0F4")
		t.SurfacePressed = ui.Hex("#E6E6EC")
		t.Border = ui.Hex("#E2E2E8")
		t.Text = ui.Hex("#17171A")
		t.TextMuted = ui.Hex("#71717A")
		t.Accent = ui.Hex("#2563EB")
		t.AccentHover = ui.Hex("#1D4ED8")
		t.AccentPressed = ui.Hex("#1E40AF")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Danger = ui.Hex("#DC2626")
		t.Warning = ui.Hex("#D97706")
		t.Success = ui.Hex("#16A34A")
		t.Selection = ui.RGBA(37, 99, 235, 0.2)
		t.Focus = ui.RGBA(37, 99, 235, 0.5)
		t.Scrollbar = ui.RGBA(0, 0, 0, 0.24)
	}
	c.SetTheme(&t)
}

// palette colors that the theme has no slot for.
func sidebarColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#17171C")
	}
	return ui.Hex("#EFEFF3")
}

func selectedRowBackground(t *ui.Theme) ui.Color {
	return t.Accent.Alpha(0.16).Over(sidebarColor(t))
}

func cardShadow(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.RGBA(0, 0, 0, 0.45)
	}
	return ui.RGBA(24, 24, 40, 0.08)
}

func codeBackground(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#0D0D11")
	}
	return ui.Hex("#15151A")
}

func codeText(t *ui.Theme) ui.Color {
	return ui.Hex("#D7D7E2")
}

var coverTints = []ui.Color{
	ui.Hex("#5B5BD6"),
	ui.Hex("#0EA5A5"),
	ui.Hex("#D97706"),
	ui.Hex("#DB2777"),
	ui.Hex("#7C3AED"),
	ui.Hex("#0284C7"),
	ui.Hex("#65A30D"),
	ui.Hex("#DC2626"),
}

// titleTint picks a stable cover color from a game's title.
func titleTint(title string) ui.Color {
	var sum uint32
	for _, r := range title {
		sum = sum*31 + uint32(r)
	}
	return coverTints[sum%uint32(len(coverTints))]
}
