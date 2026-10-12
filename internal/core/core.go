// Package core contains foundational types shared by the krkrgo runtime layers.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Errors returned by core path and storage helpers are wrapped with these
// sentinels so callers can classify invalid user input without matching text.
var (
	ErrInvalidStorageName       = errors.New("invalid storage name")
	ErrInvalidSaveDirectory     = errors.New("invalid save directory")
	ErrInvalidProjectExecutable = errors.New("invalid project executable")
)

// LocalStoragePath resolves the POSIX local-storage prefix used by Kirikiri's
// SDL ports. The value is a storage name, rather than a percent-encoded URL.
func LocalStoragePath(name string) string {
	const prefix = "file://."
	if len(name) > len(prefix) &&
		strings.EqualFold(name[:len(prefix)], prefix) &&
		(name[len(prefix)] == '/' || name[len(prefix)] == '\\') {
		return name[len(prefix):]
	}
	return name
}

// LocalStorageNativePath returns the native absolute spelling of a local file
// storage name. It removes the "file://." prefix and, on Windows, moves the
// volume letter ahead of the leading slash, so the storage spelling "/c:/games"
// addresses the drive path "c:/games". Names without a volume stay unchanged,
// which keeps the conversion idempotent.
func LocalStorageNativePath(name string) string {
	path := LocalStoragePath(name)
	if filepath.Separator != '\\' || len(path) < 4 || path[0] != '/' || path[2] != ':' || path[3] != '/' {
		return path
	}
	if !isASCIILetter(path[1]) {
		return path
	}
	return path[1:]
}

// LocalStorageName returns the storage spelling of an absolute local path:
// "file://." followed by the slash-separated path, with a Windows volume moved
// behind the leading slash so "C:/games/x" becomes "file://./c:/games/x".
// TVPNormalizeStorageName in the reference implementation writes the same shape.
func LocalStorageName(path string) string {
	path = filepath.ToSlash(path)
	if len(path) >= 3 && isASCIILetter(path[0]) && path[1] == ':' && path[2] == '/' {
		path = "/" + path
	}
	return "file://." + path
}

// StorageName normalizes an archive/storage name using ASCII case folding.
// Backslashes are accepted as native path separators, but absolute names,
// drive-qualified names, NULs, and paths escaping the storage root are not.
func StorageName(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("%w: NUL in storage name", ErrInvalidStorageName)
	}
	name = asciiLower(strings.ReplaceAll(name, `\`, "/"))
	if strings.HasPrefix(name, "/") || strings.ContainsRune(name, ':') {
		return "", fmt.Errorf("%w: absolute storage name %q", ErrInvalidStorageName, name)
	}

	parts := make([]string, 0, strings.Count(name, "/")+1)
	for _, part := range strings.Split(name, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", fmt.Errorf("%w: storage escapes root %q", ErrInvalidStorageName, name)
			}
			parts = parts[:len(parts)-1]
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: empty storage name", ErrInvalidStorageName)
	}
	return strings.Join(parts, "/"), nil
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// Limits bounds resource/index/cache allocation. Values match the Rust core
// defaults and use fixed-width integers for stable cross-language contracts.
type Limits struct {
	IndexBytes uint64 `json:"index_bytes"`
	FileBytes  uint64 `json:"file_bytes"`
	CacheBytes uint64 `json:"cache_bytes"`
	Entries    uint64 `json:"entries"`
}

// DefaultLimits returns the standard resource limits.
func DefaultLimits() Limits {
	return Limits{
		IndexBytes: 64 << 20,
		FileBytes:  512 << 20,
		CacheBytes: 64 << 20,
		Entries:    1_000_000,
	}
}

// SourceLocation identifies a source position in a storage resource.
type SourceLocation struct {
	Storage string `json:"storage"`
	Line    uint64 `json:"line"`
	Column  uint64 `json:"column"`
}

// InputEvent is a deterministic, timestamped host input event.
type InputEvent struct {
	TimeMS uint64      `json:"time_ms"`
	Action InputAction `json:"action"`
}

// InputActionType is the stable serialized discriminator for InputAction.
type InputActionType string

const (
	InputActionAdvance InputActionType = "advance"
	InputActionPointer InputActionType = "pointer"
	InputActionKey     InputActionType = "key"
)

// InputAction is one of the deterministic host actions. Only fields relevant
// to Type are serialized; this keeps its JSON representation identical to the
// Rust externally tagged enum.
type InputAction struct {
	Type    InputActionType
	X       int32
	Y       int32
	Pressed bool
	Code    string
}

// NewAdvanceAction constructs an advance action.
func NewAdvanceAction() InputAction { return InputAction{Type: InputActionAdvance} }

// NewPointerAction constructs a pointer action.
func NewPointerAction(x, y int32, pressed bool) InputAction {
	return InputAction{Type: InputActionPointer, X: x, Y: y, Pressed: pressed}
}

// NewKeyAction constructs a key action.
func NewKeyAction(code string, pressed bool) InputAction {
	return InputAction{Type: InputActionKey, Code: code, Pressed: pressed}
}

// KeyCode converts the script-visible key name used by replay input into the
// stable Windows virtual-key value consumed by the runtime.
func KeyCode(name string) (int32, bool) {
	switch name {
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return int32(name[0]), true
	case "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M",
		"N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z":
		return int32(name[0]), true
	case "F1":
		return 0x70, true
	case "F2":
		return 0x71, true
	case "F3":
		return 0x72, true
	case "F4":
		return 0x73, true
	case "F5":
		return 0x74, true
	case "F6":
		return 0x75, true
	case "F7":
		return 0x76, true
	case "F8":
		return 0x77, true
	case "F9":
		return 0x78, true
	case "F10":
		return 0x79, true
	case "F11":
		return 0x7a, true
	case "F12":
		return 0x7b, true
	case "Escape":
		return 0x1b, true
	case "Tab":
		return 0x09, true
	case "CapsLock":
		return 0x14, true
	case "Shift":
		return 0x10, true
	case "Control":
		return 0x11, true
	case "Alt":
		return 0x12, true
	case "Meta":
		return 0x5b, true
	case "Enter":
		return 0x0d, true
	case "Backspace":
		return 0x08, true
	case "Space":
		return 0x20, true
	case "Insert":
		return 0x2d, true
	case "Delete":
		return 0x2e, true
	case "Home":
		return 0x24, true
	case "End":
		return 0x23, true
	case "PageUp":
		return 0x21, true
	case "PageDown":
		return 0x22, true
	case "ArrowUp":
		return 0x26, true
	case "ArrowDown":
		return 0x28, true
	case "ArrowLeft":
		return 0x25, true
	case "ArrowRight":
		return 0x27, true
	case "Backquote":
		return 0xc0, true
	case "Backslash":
		return 0xdc, true
	case "BracketLeft":
		return 0xdb, true
	case "BracketRight":
		return 0xdd, true
	case "Comma":
		return 0xbc, true
	case "Period":
		return 0xbe, true
	case "Slash":
		return 0xbf, true
	case "Semicolon":
		return 0xba, true
	case "Quote":
		return 0xde, true
	case "Equal":
		return 0xbb, true
	case "Minus":
		return 0xbd, true
	case "PrintScreen":
		return 0x2c, true
	case "ScrollLock":
		return 0x91, true
	case "Pause":
		return 0x13, true
	case "ContextMenu":
		return 0x5d, true
	case "NumLock":
		return 0x90, true
	case "Numpad0", "Numpad1", "Numpad2", "Numpad3", "Numpad4",
		"Numpad5", "Numpad6", "Numpad7", "Numpad8", "Numpad9":
		return int32(0x60 + name[len(name)-1] - '0'), true
	case "NumpadAdd":
		return 0x6b, true
	case "NumpadSubtract":
		return 0x6d, true
	case "NumpadMultiply":
		return 0x6a, true
	case "NumpadDivide":
		return 0x6f, true
	case "NumpadDecimal":
		return 0x6e, true
	case "NumpadEnter":
		return 0x0d, true
	}
	if len(name) >= 3 && name[0] == 'F' {
		var value int32
		for i := 1; i < len(name); i++ {
			if name[i] < '0' || name[i] > '9' {
				return 0, false
			}
			value = value*10 + int32(name[i]-'0')
		}
		if value >= 13 && value <= 24 {
			return 0x6f + value, true
		}
	}
	return 0, false
}

func (a InputAction) MarshalJSON() ([]byte, error) {
	switch a.Type {
	case InputActionAdvance:
		return json.Marshal(struct {
			Type InputActionType `json:"type"`
		}{a.Type})
	case InputActionPointer:
		return json.Marshal(struct {
			Type    InputActionType `json:"type"`
			X       int32           `json:"x"`
			Y       int32           `json:"y"`
			Pressed bool            `json:"pressed"`
		}{a.Type, a.X, a.Y, a.Pressed})
	case InputActionKey:
		return json.Marshal(struct {
			Type    InputActionType `json:"type"`
			Code    string          `json:"code"`
			Pressed bool            `json:"pressed"`
		}{a.Type, a.Code, a.Pressed})
	default:
		return nil, fmt.Errorf("unknown input action type %q", a.Type)
	}
}

func (a *InputAction) UnmarshalJSON(data []byte) error {
	var typ struct {
		Type InputActionType `json:"type"`
	}
	if err := json.Unmarshal(data, &typ); err != nil {
		return err
	}
	switch typ.Type {
	case InputActionAdvance:
		*a = NewAdvanceAction()
	case InputActionPointer:
		var value struct {
			Type    InputActionType `json:"type"`
			X       int32           `json:"x"`
			Y       int32           `json:"y"`
			Pressed bool            `json:"pressed"`
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*a = NewPointerAction(value.X, value.Y, value.Pressed)
	case InputActionKey:
		var value struct {
			Type    InputActionType `json:"type"`
			Code    string          `json:"code"`
			Pressed bool            `json:"pressed"`
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*a = NewKeyAction(value.Code, value.Pressed)
	default:
		return fmt.Errorf("unknown input action type %q", typ.Type)
	}
	return nil
}
