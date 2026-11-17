package krkrgocli

import (
	"testing"

	"github.com/eslzzyl/krkrgo/internal/runtime"
)

func TestParseLaunchArgsDefaultsToNativeWindowScale(t *testing.T) {
	opts, err := ParseLaunchArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.WindowScale != 1 {
		t.Fatalf("default window scale = %g, want 1", opts.WindowScale)
	}
}

func TestParseLaunchArgsAnime4K(t *testing.T) {
	opts, err := ParseLaunchArgs([]string{"-anime4k"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Anime4K {
		t.Fatal("Anime4K flag was not parsed")
	}
}

func TestParseLaunchArgsUpscaler(t *testing.T) {
	for _, mode := range []string{"bilinear", "bicubic", "anime4k"} {
		opts, err := ParseLaunchArgs([]string{"-upscaler", mode})
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if opts.Upscaler != mode {
			t.Fatalf("upscaler = %q, want %q", opts.Upscaler, mode)
		}
	}
	if _, err := ParseLaunchArgs([]string{"-upscaler", "nearest"}); err == nil {
		t.Fatal("unsupported upscaler succeeded")
	}
	if _, err := ParseLaunchArgs([]string{"-anime4k", "-upscaler", "bilinear"}); err == nil {
		t.Fatal("conflicting upscaler flags succeeded")
	}
}

func TestParseLaunchArgsNormalizesInvalidWindowScale(t *testing.T) {
	for _, value := range []string{"0", "-2"} {
		opts, err := ParseLaunchArgs([]string{"-window-scale", value})
		if err != nil {
			t.Fatal(err)
		}
		if opts.WindowScale != 1 {
			t.Fatalf("window scale for %s = %g, want 1", value, opts.WindowScale)
		}
	}
}

func TestFinishSessionDefaultsSceneAnimationArgument(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		gameArguments := map[string]string{"-debugwin": "no"}
		session, err := finishSession(runtime.NewSession(runtime.DefaultServices(), unlimitedBudget), "", gameArguments)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if got := session.Services.Arguments["-scanim"]; got != "no" {
			t.Fatalf("default -scanim = %q, want %q", got, "no")
		}
		if _, exists := gameArguments["-scanim"]; exists {
			t.Fatal("finishSession mutated caller game arguments")
		}
	})

	t.Run("explicit", func(t *testing.T) {
		gameArguments := map[string]string{"-scanim": "yes"}
		session, err := finishSession(runtime.NewSession(runtime.DefaultServices(), unlimitedBudget), "", gameArguments)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		if got := session.Services.Arguments["-scanim"]; got != "yes" {
			t.Fatalf("explicit -scanim = %q, want %q", got, "yes")
		}
		if gameArguments["-scanim"] != "yes" {
			t.Fatal("finishSession changed caller -scanim")
		}
	})
}
