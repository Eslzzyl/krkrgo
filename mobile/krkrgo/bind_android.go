//go:build android

package krkrgo

import "github.com/hajimehoshi/ebiten/v2/mobile"

func init() {
	mobile.SetGame(globalGame)
}
