package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// SaveDirectory selects the game's existing savedata directory by default.
// An explicit override may select a new isolated directory, but an override
// inside the installation is accepted only below savedata. Existing ancestors
// are resolved first so symlinked paths cannot redirect saves into the game.
func SaveDirectory(project string, overridePath *string) (string, error) {
	lexicalProject, err := absolutePath(project)
	if err != nil {
		return "", err
	}
	resolvedProject, err := canonicalPath(lexicalProject)
	if err != nil {
		return "", err
	}

	result := filepath.Join(lexicalProject, "savedata")
	if overridePath != nil {
		result = *overridePath
		if !filepath.IsAbs(result) {
			// Rust resolves relative overrides against the process working
			// directory, not against the project directory.
			result, err = absolutePath(result)
			if err != nil {
				return "", fmt.Errorf("%w: %v", ErrInvalidSaveDirectory, err)
			}
		} else {
			result = filepath.Clean(result)
		}
	}
	resolvedResult, err := resolveExistingAncestors(result)
	if err != nil {
		return "", err
	}

	savedata := filepath.Join(resolvedProject, "savedata")
	if pathWithin(resolvedResult, resolvedProject) && !pathWithin(resolvedResult, savedata) {
		return "", fmt.Errorf("%w: path must be in savedata or outside project", ErrInvalidSaveDirectory)
	}
	return filepath.Clean(result), nil
}

func absolutePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// resolveExistingAncestors follows every existing ancestor, then appends the
// non-existing suffix without creating it.
func resolveExistingAncestors(path string) (string, error) {
	resolved, err := resolvePathWithMissingSuffix(path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidSaveDirectory, err)
	}
	return resolved, nil
}

func resolvePathWithMissingSuffix(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := filepath.Clean(absolute)
	suffix := make([]string, 0, 4)
	for {
		if _, err := os.Stat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", fmt.Errorf("resolve ancestor: %w", err)
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect ancestor: %w", err)
		}
		parent := filepath.Dir(ancestor)
		base := filepath.Base(ancestor)
		if parent == ancestor || base == "." || base == string(filepath.Separator) || base == "" {
			return "", fmt.Errorf("invalid save path")
		}
		suffix = append(suffix, base)
		ancestor = parent
	}
}

// LocalStorageRelativePath resolves a local file URI or absolute path against
// root. Existing symlinks are resolved before checking containment; missing
// suffixes remain lexical so callers can create or query them safely.
func LocalStorageRelativePath(name, root string) (string, bool, error) {
	if root == "" {
		return "", false, nil
	}
	path := filepath.FromSlash(strings.ReplaceAll(LocalStorageNativePath(name), `\`, "/"))
	if !filepath.IsAbs(path) {
		return "", false, nil
	}
	resolvedPath, err := resolvePathWithMissingSuffix(path)
	if err != nil {
		return "", false, err
	}
	resolvedRoot, err := resolvePathWithMissingSuffix(root)
	if err != nil {
		return "", false, err
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil {
		return "", false, err
	}
	if !pathWithin(resolvedPath, resolvedRoot) {
		return "", false, nil
	}
	return filepath.ToSlash(relative), true, nil
}

// pathWithin is component-aware (unlike a string prefix), and treats path ==
// root as within root.
func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ProjectExecutable selects project metadata without executing a Windows
// binary. An explicit choice wins; otherwise a directory-matching executable
// is preferred, followed by a single non-helper executable.
func ProjectExecutable(project string, explicit *string) (string, bool, error) {
	lexicalProject, err := absolutePath(project)
	if err != nil {
		return "", false, err
	}
	resolvedProject, err := canonicalPath(lexicalProject)
	if err != nil {
		return "", false, err
	}
	if explicit != nil {
		candidate := *explicit
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(lexicalProject, candidate)
		}
		candidate = filepath.Clean(candidate)
		resolvedCandidate, resolveErr := canonicalPath(candidate)
		if resolveErr != nil {
			return "", false, fmt.Errorf("%w: %v", ErrInvalidProjectExecutable, resolveErr)
		}
		info, statErr := os.Stat(resolvedCandidate)
		if statErr != nil || !info.Mode().IsRegular() || !pathWithin(resolvedCandidate, resolvedProject) {
			return "", false, fmt.Errorf("%w: executable must be a file inside project", ErrInvalidProjectExecutable)
		}
		return candidate, true, nil
	}

	directory := normalizeProjectName(filepath.Base(lexicalProject))
	type candidate struct {
		preferred bool
		path      string
	}
	candidates := make([]candidate, 0)
	entries, err := os.ReadDir(lexicalProject)
	if err != nil {
		return "", false, err
	}
	for _, entry := range entries {
		path := filepath.Join(lexicalProject, entry.Name())
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".exe") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if normalizeProjectName(stem) == directory {
			candidates = append(candidates, candidate{preferred: true, path: path})
		} else if !isHelperExecutable(stem) {
			candidates = append(candidates, candidate{path: path})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].preferred != candidates[j].preferred {
			return candidates[i].preferred
		}
		return candidates[i].path < candidates[j].path
	})
	if len(candidates) == 1 || (len(candidates) > 0 && candidates[0].preferred && (len(candidates) == 1 || !candidates[1].preferred)) {
		return candidates[0].path, true, nil
	}
	return "", false, nil
}

func normalizeProjectName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteString(strings.ToLower(string(r)))
		}
	}
	return b.String()
}

func isHelperExecutable(stem string) bool {
	lower := strings.ToLower(stem)
	for _, prefix := range []string{"unins", "setup", "install", "updat", "patch", "config", "setting"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
