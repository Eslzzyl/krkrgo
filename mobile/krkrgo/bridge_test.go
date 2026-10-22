package krkrgo

import "testing"

func TestMobileGameUsesDefaultLayoutBeforeOpen(t *testing.T) {
	game := &mobileGame{owner: &controller{}}
	if width, height := game.Layout(0, 0); width != 1280 || height != 720 {
		t.Fatalf("default mobile layout = %dx%d, want 1280x720", width, height)
	}
}

func TestCloseGameWithoutOpenIsSafe(t *testing.T) {
	controller := &controller{}
	controller.closeGame()
}
