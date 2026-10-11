package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/host"
)

func TestRunExtractXP3(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "data.xp3")
	files := map[string][]byte{
		"Dir/Startup.TJS":                  []byte("startup script"),
		"LeGaL/" + strings.Repeat("X", 256): []byte("unrepresentable name"),
	}
	if err := os.WriteFile(archivePath, toolTestXP3Files(files), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "extracted")
	var stderr bytes.Buffer
	if err := run([]string{"extract", "--archive", archivePath, "--output", output}, strings.NewReader(""), &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(output, "dir", "startup.tjs"))
	if err != nil || string(got) != "startup script" {
		t.Fatalf("extracted contents = %q, %v; want startup script", got, err)
	}
	var skipped []skippedEntry
	manifest, err := os.ReadFile(filepath.Join(output, "__xp3_extract_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &skipped); err != nil || len(skipped) != 1 {
		t.Fatalf("manifest = %s, %v; want one skipped entry", manifest, err)
	}
	longName := "LeGaL/" + strings.Repeat("X", 256)
	if skipped[0].Name != longName || skipped[0].Hash != assets.Adler32([]byte("unrepresentable name")) ||
		skipped[0].Size != uint64(len("unrepresentable name")) || skipped[0].Reason == "" {
		t.Fatalf("skipped entry = %#v; want exact name, hash, size, and reason", skipped[0])
	}
	if !strings.Contains(stderr.String(), "warning: skipped XP3 entry") {
		t.Fatalf("extract warnings = %q; want skipped-entry warning", stderr.String())
	}
	if err := run([]string{"extract", "--archive", archivePath, "--output", output}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("extract accepted an existing output directory")
	}
}


func toolTestXP3Files(files map[string][]byte) []byte {
	index := make([]byte, 0)
	payload := make([]byte, 0)
	for name, data := range files {
		nameUnits := utf16.Encode([]rune(name))
		info := toolTestU32(nil, 0)
		info = toolTestU64(info, uint64(len(data)))
		info = toolTestU64(info, uint64(len(data)))
		info = toolTestU16(info, uint16(len(nameUnits)))
		for _, unit := range nameUnits {
			info = toolTestU16(info, unit)
		}
		entry := toolTestChunk(nil, "info", info)
		segment := toolTestU32(nil, 0)
		segment = toolTestU64(segment, uint64(len(assets.MAGIC)+8+len(payload)))
		segment = toolTestU64(segment, uint64(len(data)))
		segment = toolTestU64(segment, uint64(len(data)))
		entry = toolTestChunk(entry, "segm", segment)
		entry = toolTestChunk(entry, "adlr", toolTestU32(nil, assets.Adler32(data)))
		index = toolTestChunk(index, "File", entry)
		payload = append(payload, data...)
	}

	archive := append([]byte(nil), assets.MAGIC...)
	archive = append(archive, make([]byte, 8)...)
	archive = append(archive, payload...)
	binary.LittleEndian.PutUint64(archive[len(assets.MAGIC):], uint64(len(assets.MAGIC)+8+len(payload)))
	archive = append(archive, 0)
	archive = toolTestU64(archive, uint64(len(index)))
	return append(archive, index...)
}


func toolTestChunk(dst []byte, tag string, data []byte) []byte {
	dst = append(dst, tag...)
	dst = toolTestU64(dst, uint64(len(data)))
	return append(dst, data...)
}

func toolTestU16(dst []byte, value uint16) []byte {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], value)
	return append(dst, b[:]...)
}

func toolTestU32(dst []byte, value uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], value)
	return append(dst, b[:]...)
}

func toolTestU64(dst []byte, value uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], value)
	return append(dst, b[:]...)
}

func TestRunWritesPartialReplayResultOnError(t *testing.T) {
	for _, outputMode := range []string{"stdout", "file"} {
		t.Run(outputMode, func(t *testing.T) {
			project := t.TempDir()
			if err := os.WriteFile(filepath.Join(project, "startup.tjs"), []byte("root = new Layer();\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			outputPath := "-"
			if outputMode == "file" {
				outputPath = filepath.Join(project, "partial-result.json")
			}
			input := `[
				{"at_ms":0,"action":"checkpoint","name":"before error","inspect":["1 + 1"]},
				{"at_ms":5,"action":"assert","expression":"(function(){throw new Exception(\"replay failure\");})()"}
			]`
			var stdout, stderr bytes.Buffer
			err := run([]string{"replay", "--project", project, "--input", "-", "--output", outputPath, "--step-ms", "5"}, strings.NewReader(input), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "replay action 1 at 5 ms") {
				t.Fatalf("run error = %v, want replay action failure", err)
			}

			resultJSON := stdout.Bytes()
			if outputMode == "file" {
				resultJSON, err = os.ReadFile(outputPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			var partial host.ReplayResult
			if err := json.Unmarshal(resultJSON, &partial); err != nil {
				t.Fatalf("partial output %q is not replay JSON: %v", resultJSON, err)
			}
			if partial.FinalTimeMS != 5 || len(partial.Frames) != 2 || len(partial.Checkpoints) != 1 || partial.Checkpoints[0].Name != "before error" {
				t.Fatalf("partial replay result = %#v, want 2 frames and the completed checkpoint", partial)
			}
			if stderr.Len() != 0 {
				t.Fatalf("run wrote error to stderr directly: %q", stderr.String())
			}
		})
	}
}
