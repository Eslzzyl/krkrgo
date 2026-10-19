package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/pprof"
	"sort"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/host"
	"github.com/eslzzyl/krkrgo/internal/runtime"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "krkrgo-tool:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: krkrgo-tool replay|extract [options]")
	}
	switch args[0] {
	case "replay":
		return runReplay(args[1:], stdin, stdout, stderr)
	case "extract":
		return runExtract(args[1:], stderr)
	default:
		return errors.New("usage: krkrgo-tool replay|extract [options]")
	}
}

func runExtract(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	fs.SetOutput(stderr)
	archivePath := fs.String("archive", "", "XP3 archive to extract (required)")
	outputPath := fs.String("output", "", "new output directory (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *archivePath == "" || *outputPath == "" {
		return errors.New("extract requires --archive FILE and --output DIR")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return extractXP3(*archivePath, *outputPath, stderr)
}

func extractXP3(archivePath, outputPath string, warnings io.Writer) (err error) {
	archive, err := assets.OpenArchive(archivePath, core.DefaultLimits())
	if err != nil {
		return fmt.Errorf("open XP3 archive: %w", err)
	}
	defer archive.Close()

	if err := os.Mkdir(outputPath, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(outputPath)
		}
	}()

	names := make([]string, 0, len(archive.Entries))
	for name := range archive.Entries {
		names = append(names, name)
	}
	sort.Strings(names)
	skipped := make([]skippedEntry, 0)
	for _, name := range names {
		entry := archive.Entries[name]
		data, readErr := archive.Read(name)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", name, readErr)
		}
		if assets.Adler32(data) != entry.Hash {
			return fmt.Errorf("verify %s: checksum mismatch", name)
		}
		target := filepath.Join(outputPath, filepath.FromSlash(name))
		reason := unrepresentablePathReason(name)
		if reason != "" {
			skipped = append(skipped, skippedEntry{Name: entry.OriginalName, Hash: entry.Hash, Size: entry.Size, Reason: reason})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create directory for %s: %w", name, err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	if len(skipped) != 0 {
		manifestName := chooseManifestName(names)
		manifest, err := json.MarshalIndent(skipped, "", "  ")
		if err != nil {
			return fmt.Errorf("encode extraction manifest: %w", err)
		}
		if err := os.WriteFile(filepath.Join(outputPath, manifestName), manifest, 0o644); err != nil {
			return fmt.Errorf("write extraction manifest: %w", err)
		}
		for _, entry := range skipped {
			if _, err := fmt.Fprintf(warnings, "warning: skipped XP3 entry %q (%s); see %s\n", entry.Name, entry.Reason, manifestName); err != nil {
				return fmt.Errorf("write extraction warning: %w", err)
			}
		}
	}
	return nil
}

type skippedEntry struct {
	Name   string `json:"name"`
	Hash   uint32 `json:"adler32"`
	Size   uint64 `json:"size"`
	Reason string `json:"reason"`
}

func unrepresentablePathReason(name string) string {
	for _, component := range strings.Split(name, "/") {
		if len(component) > 255 {
			return fmt.Sprintf("path component is %d bytes; exceeds the portable 255-byte component limit", len(component))
		}
	}
	return ""
}

func chooseManifestName(names []string) string {
	occupied := make(map[string]struct{}, len(names))
	for _, name := range names {
		occupied[name] = struct{}{}
	}
	for suffix := 0; ; suffix++ {
		name := "__xp3_extract_manifest.json"
		if suffix != 0 {
			name = fmt.Sprintf("__xp3_extract_manifest_%d.json", suffix)
		}
		if _, exists := occupied[name]; !exists {
			return name
		}
	}
}

func runReplay(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	fs.SetOutput(stderr)
	project := fs.String("project", "", "game installation directory (required)")
	saveDir := fs.String("save-dir", "", "save-data directory")
	inputPath := fs.String("input", "-", "replay JSON file, or - for stdin")
	outputPath := fs.String("output", "-", "result JSON file, or - for stdout")
	stepMS := fs.Uint64("step-ms", 16, "deterministic tick step in milliseconds")
	advanceMS := fs.Uint64("advance-ms", 0, "optional final clock time")
	budget := fs.Uint64("budget", 1<<20, "session instruction budget")
	memProfile := fs.String("memprofile", "", "write an in-use heap profile after the replay")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *project == "" {
		return errors.New("--project is required; replay does not execute without a project")
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}

	var input io.Reader = stdin
	var inputFile *os.File
	if *inputPath != "" && *inputPath != "-" {
		f, err := os.Open(*inputPath)
		if err != nil {
			return fmt.Errorf("open replay input: %w", err)
		}
		defer f.Close()
		input, inputFile = f, f
	}
	_ = inputFile
	replay, err := host.ReadReplay(input)
	if err != nil {
		return err
	}

	session, err := runtime.OpenSession(*project, *saveDir, *budget)
	if err != nil {
		return fmt.Errorf("open project: %w", err)
	}
	defer session.Close()
	if *memProfile != "" {
		defer func() {
			if err := writeHeapProfile(*memProfile); err != nil {
				fmt.Fprintf(stderr, "write memory profile: %v\n", err)
			}
		}()
	}
	var end []uint64
	if *advanceMS != 0 {
		end = []uint64{*advanceMS}
	}
	if err := replay.Validate(session, end...); err != nil {
		return err
	}
	startupMessageStart := len(session.Services.Messages)
	if _, err := session.Startup(); err != nil {
		return host.WrapReplayError(session, "startup", err, startupMessageStart)
	}
	result, err := replay.Run(session, *stepMS)
	if err != nil {
		if writeErr := writeReplayResult(*outputPath, stdout, result); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		return err
	}
	if *advanceMS != 0 {
		current := session.Services.TimeMS
		messageStart := len(session.Services.Messages)
		if err := session.Tick(*advanceMS); err != nil {
			context := fmt.Sprintf("advance from %d ms to %d ms", current, *advanceMS)
			replayErr := host.WrapReplayError(session, context, err, messageStart)
			if writeErr := writeReplayResult(*outputPath, stdout, result); writeErr != nil {
				return errors.Join(replayErr, writeErr)
			}
			return replayErr
		}
		result.FinalTimeMS = session.Services.TimeMS
	}
	return writeReplayResult(*outputPath, stdout, result)
}

func writeHeapProfile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	goruntime.GC()
	if err := pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeReplayResult(outputPath string, stdout io.Writer, result host.ReplayResult) error {
	var output io.Writer = stdout
	if outputPath != "" && outputPath != "-" {
		f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
		if err != nil {
			return fmt.Errorf("open replay output: %w", err)
		}
		defer f.Close()
		output = f
	}
	return host.MarshalResult(output, result)
}
