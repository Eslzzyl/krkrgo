package krkrgocli

import "testing"

func TestParseLaunchArgsAnime4K(t *testing.T) {
	opts, err := ParseLaunchArgs([]string{"-anime4k", "-render-scale", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Anime4K {
		t.Fatal("Anime4K flag was not parsed")
	}
	if opts.RenderScale != 2 {
		t.Fatalf("render scale = %d, want 2", opts.RenderScale)
	}
}

func TestParseLaunchArgsUpscaler(t *testing.T) {
	for _, mode := range []string{"bilinear", "bicubic", "anime4k"} {
		opts, err := ParseLaunchArgs([]string{"-upscaler", mode, "-render-scale", "2"})
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
