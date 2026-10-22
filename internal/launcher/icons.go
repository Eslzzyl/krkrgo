package launcher

import (
	"github.com/egoist/mygo/ui"
	goicons "github.com/opengoui/icons"
	"github.com/opengoui/icons/name"
)

func mustIcon(iconName string) *ui.SVG {
	data, err := goicons.SVG(iconName)
	if err != nil {
		panic(err)
	}
	return ui.MustParseSVG(data)
}

var (
	iconAdd     = mustIcon(name.Plus)
	iconRefresh = mustIcon(name.RefreshCw)
	iconFolder  = mustIcon(name.Folder)
	iconArchive = mustIcon(name.Archive)
	iconLog     = mustIcon(name.FileText)
	iconPencil  = mustIcon(name.Pencil)
	iconImage   = mustIcon(name.Image)
	iconGamepad = mustIcon(name.Gamepad2)
)
