package main

import (
	"os"

	"github.com/eslzzyl/krkrgo/internal/krkrgocli"
	"github.com/eslzzyl/krkrgo/internal/launcher"
	_ "github.com/eslzzyl/krkrgo/internal/launcher/locales"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		krkrgocli.RunGame(os.Args[2:])
		return
	}
	launcher.Run()
}
