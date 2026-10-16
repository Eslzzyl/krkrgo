package runtime

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/eslzzyl/krkrgo/internal/core"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	systemVersionString      = "krkrgo"
	systemVersionInformation = "krkrgo Go runtime; C++ System compatibility surface"
	compactLevelIdle         = 5
	compactLevelDeactivate   = 10
)

func sameContinuousHandler(a, b tjs.Value) bool {
	return a.Kind() == tjs.KindObject && b.Kind() == tjs.KindObject && a.ObjectRef().SameDispatch(b.ObjectRef())
}

var ErrSystemExit = fmt.Errorf("system exit requested")

func systemPlatformName() string { return stdruntime.GOOS + "/" + stdruntime.GOARCH }

func systemOSName() string {
	switch stdruntime.GOOS {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	case "netbsd":
		return "NetBSD"
	default:
		return stdruntime.GOOS
	}
}

func systemActualColor(value int64) int64 {
	// TVPToActualColor resolves high-byte system color identifiers through the
	// platform color table. Go has no equivalent table, so preserve the RGB
	// payload and discard the marker rather than inventing a platform mapping.
	return value & 0x00ffffff
}
func systemDirectoryPath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}

func systemFilePath(path string) string {
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return path
}

// Kirikiri local paths use the storage spelling "file://." followed by the
// slash-normalized absolute path and a trailing slash (for example,
// "/games/title/savedata" becomes "file://./games/title/savedata/", and a
// Windows drive path becomes "file://./c:/games/title/savedata/").
func systemFileURI(path string) string {
	if path == "" {
		return "file://./"
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return "file://./"
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return core.LocalStorageName(path)
}

func (s *Services) systemPath(operation string) string {
	if s == nil {
		if operation == "get:dataPath" || operation == "get:personalPath" || operation == "get:appDataPath" {
			return systemFileURI("")
		}
		return ""
	}
	s.mu.RLock()
	storage, saveDir, exeName := s.Storage, s.SaveDir, s.systemExecutableName
	s.mu.RUnlock()
	switch operation {
	case "get:exePath":
		if storage == nil {
			return ""
		}
		return systemDirectoryPath(storage.Project)
	case "get:exeName":
		return systemFilePath(exeName)
	case "get:dataPath", "get:personalPath", "get:appDataPath":
		return systemFileURI(saveDir)
	default:
		return ""
	}
}

func (s *Services) systemNative(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if s == nil {
		return tjs.VoidValue, fmt.Errorf("System.%s requires services", operation)
	}
	s.ensureInit()
	arg := func(index int) (tjs.Value, error) {
		if index >= len(args) {
			return tjs.VoidValue, fmt.Errorf("System.%s: missing argument %d", operation, index)
		}
		return args[index], nil
	}
	intArg := func(index int) (int64, error) {
		value, err := arg(index)
		if err != nil {
			return 0, err
		}
		return value.Int()
	}
	boolArg := func(index int) (bool, error) {
		value, err := arg(index)
		if err != nil {
			return false, err
		}
		return value.Truth()
	}

	switch operation {
	case "readRegValue":
		if len(args) != 1 || args[0].Kind() != tjs.KindString {
			return tjs.VoidValue, fmt.Errorf("System.readRegValue requires one registry key string")
		}
		value, ok := readSystemRegistryValue(args[0].Text())
		if !ok {
			return tjs.VoidValue, nil
		}
		return value, nil
	case "addFont":
		linked := false
		for _, plugin := range s.Plugins {
			if pluginLinkKey(plugin) == "addfont.dll" {
				linked = true
				break
			}
		}
		if !linked {
			return tjs.VoidValue, fmt.Errorf("System.addFont requires addFont.dll to be linked")
		}
		if len(args) < 1 || len(args) > 2 {
			return tjs.VoidValue, fmt.Errorf("System.addFont requires a storage name and optional private flag")
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("System.addFont %q: %w", args[0].Text(), err)
		}
		count, err := s.registerFontData(data)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("System.addFont %q: %w", args[0].Text(), err)
		}
		return tjs.IntegerValue(int64(count)), nil
	case "getArgument":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name, err := value.Unary("string")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.RLock()
		argument, exists := s.Arguments[name.Text()]
		s.mu.RUnlock()
		if !exists {
			return tjs.VoidValue, nil
		}
		return tjs.StringValue(argument), nil
	case "setArgument":
		nameValue, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name, err := nameValue.Unary("string")
		if err != nil {
			return tjs.VoidValue, err
		}
		value, err := arg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		text, err := value.Unary("string")
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.Arguments[name.Text()] = text.Text()
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "createAppLock":
		key, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name := key.Text()
		if nul := strings.IndexByte(name, 0); nul >= 0 {
			name = name[:nul]
		}
		s.mu.Lock()
		_, exists := s.appLocks[name]
		if !exists {
			s.appLocks[name] = struct{}{}
		}
		s.mu.Unlock()
		return tjs.IntegerValue(systemBoolInt(!exists)), nil
	case "inform":
		if len(args) < 1 || len(args) > 2 {
			return tjs.VoidValue, fmt.Errorf("System.inform requires text and an optional caption")
		}
		s.Messages = append(s.Messages, args[0].Text())
		return tjs.VoidValue, nil
	case "getKeyState":
		key, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		current := true
		if len(args) > 1 {
			current, err = boolArg(1)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		return tjs.IntegerValue(systemBoolInt(s.inputKeyState(uint32(key), current))), nil
	case "getTickCount", "getTickCount64":
		return tjs.IntegerValue(s.systemTickCount()), nil
	case "get:screenWidth":
		return tjs.IntegerValue(int64(s.Display.PrimaryDisplay().Bounds[2])), nil
	case "get:screenHeight":
		return tjs.IntegerValue(int64(s.Display.PrimaryDisplay().Bounds[3])), nil
	case "get:desktopLeft":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[0])), nil
	case "get:desktopTop":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[1])), nil
	case "get:desktopWidth":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[2])), nil
	case "get:desktopHeight":
		return tjs.IntegerValue(int64(s.Display.DesktopBounds()[3])), nil
	case "get:exePath", "get:exeName", "get:dataPath", "get:personalPath", "get:appDataPath":
		return tjs.StringValue(s.systemPath(operation)), nil
	case "get:title":
		return tjs.StringValue(s.systemTitle()), nil
	case "set:title":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.setSystemTitle(value.Text())
		return tjs.VoidValue, nil
	case "get:graphicCacheLimit":
		return tjs.IntegerValue(int64(s.Limits.CacheBytes)), nil
	case "set:graphicCacheLimit":
		value, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		// C++ receives this property as a signed tjs_int before converting the
		// value to unsigned bytes. Every negative value selects the system limit.
		value = int64(int32(value))
		if value < 0 || uint64(value) > s.graphicCacheSystemLimit {
			s.Limits.CacheBytes = s.graphicCacheSystemLimit
		} else {
			s.Limits.CacheBytes = uint64(value)
		}
		return tjs.VoidValue, nil
	case "terminate":
		code := int64(0)
		if len(args) > 0 && !args[0].IsVoid() {
			var err error
			code, err = intArg(0)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		s.Terminated, s.TerminationCode = true, code
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "exit":
		code := int64(0)
		if len(args) > 0 && !args[0].IsVoid() {
			var err error
			code, err = intArg(0)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		s.mu.Lock()
		s.Terminated, s.TerminationCode = true, code
		s.mu.Unlock()
		// A Go library cannot terminate its hosting process synchronously.
		return tjs.VoidValue, fmt.Errorf("%w: code %d", ErrSystemExit, code)
	case "addContinuousHandler":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("System.addContinuousHandler requires a callback")
		}
		callback := args[0]
		if callback.Kind() != tjs.KindObject || callback.IsNull() {
			return tjs.VoidValue, fmt.Errorf("System.addContinuousHandler requires a callback object")
		}
		if vm == nil || s.callbackValueValid(vm, callback) == false {
			return tjs.VoidValue, fmt.Errorf("System.addContinuousHandler requires a callable callback")
		}
		s.mu.Lock()
		for _, existing := range s.ContinuousHandlers {
			if sameContinuousHandler(existing, callback) {
				s.mu.Unlock()
				return tjs.VoidValue, nil
			}
		}
		s.ContinuousHandlers = append(s.ContinuousHandlers, callback)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "removeContinuousHandler":
		if len(args) < 1 {
			return tjs.VoidValue, fmt.Errorf("System.removeContinuousHandler requires a callback")
		}
		callback := args[0]
		s.mu.Lock()
		for i, existing := range s.ContinuousHandlers {
			if sameContinuousHandler(existing, callback) {
				s.ContinuousHandlers = append(s.ContinuousHandlers[:i], s.ContinuousHandlers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "toActualColor":
		value, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(systemActualColor(value)), nil
	case "clearGraphicCache":
		if s.Storage == nil {
			return tjs.VoidValue, UnsupportedDevice("graphic cache")
		}
		s.Storage.ClearCache()
		return tjs.VoidValue, nil
	case "touchImages":
		if len(args) < 1 || len(args) > 3 {
			return tjs.VoidValue, fmt.Errorf("System.touchImages requires an array and optional limit and timeout")
		}
		if vm == nil {
			return tjs.VoidValue, fmt.Errorf("System.touchImages requires a VM")
		}
		items, err := vm.ArrayItems(args[0])
		if err != nil {
			return tjs.VoidValue, err
		}
		limit := int64(0)
		if len(args) >= 2 && !args[1].IsVoid() {
			limit, err = args[1].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		timeout := uint64(0)
		if len(args) >= 3 && !args[2].IsVoid() {
			n, e := args[2].Int()
			if e != nil {
				return tjs.VoidValue, e
			}
			if n < 0 {
				return tjs.VoidValue, fmt.Errorf("negative touchImages timeout")
			}
			timeout = uint64(n)
		}
		if s.Storage == nil {
			return tjs.VoidValue, UnsupportedDevice("graphic storage")
		}
		cacheLimit := s.Limits.CacheBytes
		if cacheLimit == 0 {
			return tjs.VoidValue, nil
		}
		maxBytes := cacheLimit
		if limit > 0 && uint64(limit) < maxBytes {
			maxBytes = uint64(limit)
		} else if limit < 0 {
			remaining := uint64(-(limit + 1)) + 1
			if remaining >= cacheLimit {
				return tjs.VoidValue, nil
			}
			maxBytes = cacheLimit - remaining
		}
		start := time.Now()
		var loaded uint64
		for _, item := range items {
			if item.IsVoid() {
				break
			}
			if timeout != 0 && uint64(time.Since(start)/time.Millisecond) >= timeout {
				break
			}
			if loaded >= maxBytes {
				break
			}
			data, e := s.Storage.Read(item.Text())
			if e != nil {
				return tjs.VoidValue, e
			}
			loaded += uint64(len(data))
		}
		return tjs.VoidValue, nil
	case "createUUID":
		var raw [16]byte
		if _, err := cryptorand.Read(raw[:]); err != nil {
			return tjs.VoidValue, fmt.Errorf("create UUID: %w", err)
		}
		raw[6] = raw[6]&0x0f | 0x40
		raw[8] = raw[8]&0x3f | 0x80
		return tjs.StringValue(hex.EncodeToString(raw[0:4]) + "-" + hex.EncodeToString(raw[4:6]) + "-" + hex.EncodeToString(raw[6:8]) + "-" + hex.EncodeToString(raw[8:10]) + "-" + hex.EncodeToString(raw[10:16])), nil
	case "assignMessage":
		if len(args) < 2 {
			return tjs.VoidValue, fmt.Errorf("System.assignMessage requires id and message")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.MessageMap == nil {
			return tjs.IntegerValue(0), nil
		}
		id, message := args[0].Text(), args[1].Text()
		if _, ok := s.MessageMap[id]; !ok {
			return tjs.IntegerValue(0), nil
		}
		s.MessageMap[id] = message
		return tjs.IntegerValue(1), nil
	case "doCompact":
		level := int64(100)
		if len(args) > 0 && !args[0].IsVoid() {
			var err error
			level, err = intArg(0)
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if level >= compactLevelDeactivate && s.Storage != nil {
			s.Storage.ClearCache()
		}
		return tjs.VoidValue, nil
	case "getAboutString":
		return tjs.StringValue(systemVersionInformation), nil
	case "get:versionString":
		return tjs.StringValue(systemVersionString), nil
	case "get:versionInformation":
		return tjs.StringValue(systemVersionInformation), nil
	case "get:eventDisabled":
		s.mu.RLock()
		value := s.EventDisabled
		s.mu.RUnlock()
		return tjs.IntegerValue(systemBoolInt(value)), nil
	case "set:eventDisabled":
		value, err := boolArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.EventDisabled = value
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:platformName":
		return tjs.StringValue(systemPlatformName()), nil
	case "get:osName":
		return tjs.StringValue(systemOSName()), nil
	case "get:exitOnWindowClose":
		s.mu.RLock()
		value := s.ExitOnWindowClose
		s.mu.RUnlock()
		return tjs.IntegerValue(systemBoolInt(value)), nil
	case "set:exitOnWindowClose":
		value, err := boolArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.ExitOnWindowClose = value
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:drawThreadNum":
		s.mu.RLock()
		value := s.DrawThreadNum
		s.mu.RUnlock()
		return tjs.IntegerValue(value), nil
	case "set:drawThreadNum":
		value, err := intArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.DrawThreadNum = value
		s.mu.Unlock()
		return tjs.VoidValue, nil
	case "get:processorNum":
		return tjs.IntegerValue(int64(stdruntime.NumCPU())), nil
	case "get:exeBits", "get:osBits":
		return tjs.IntegerValue(int64(strconv.IntSize)), nil
	case "get:exitOnNoWindowStartup":
		s.mu.RLock()
		value := s.ExitOnNoWindowStartup
		s.mu.RUnlock()
		return tjs.IntegerValue(systemBoolInt(value)), nil
	case "set:exitOnNoWindowStartup":
		value, err := boolArg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.mu.Lock()
		s.ExitOnNoWindowStartup = value
		s.mu.Unlock()
		return tjs.VoidValue, nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: System.%s", ErrUnsupportedNative, operation)
	}
}

func (s *Services) callbackValueValid(vm *tjs.VM, value tjs.Value) bool {
	if vm == nil || value.Kind() != tjs.KindObject || value.IsNull() {
		return false
	}
	object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
	if !ok || object == nil || !object.IsValid() {
		return false
	}
	return object.Category == tjs.CategoryFunction || object.Category == tjs.CategoryNative || object.Category == tjs.CategoryMethod
}

func continuousHandlerValid(vm *tjs.VM, callback tjs.Value, ref tjs.ObjectRef) bool {
	if vm.IsValid(callback).IntegerValue() == 0 {
		return false
	}
	if ref.HasBoundContext() && vm.IsValid(tjs.ObjectValue(ref.BoundContextRef())).IntegerValue() == 0 {
		return false
	}
	return true
}

func (s *Services) invokeContinuousHandlers(vm *tjs.VM, tick uint64, budget *uint64) error {
	if vm == nil {
		return fmt.Errorf("continuous handler requires a VM")
	}
	s.mu.RLock()
	if s.EventDisabled {
		s.mu.RUnlock()
		return nil
	}
	// Keep the common case on the stack while retaining the snapshot semantics:
	// callbacks may add/remove handlers without changing this tick's iteration.
	var smallHandlers [32]tjs.Value
	handlers := s.ContinuousHandlers
	if len(handlers) <= len(smallHandlers) {
		handlers = smallHandlers[:len(handlers)]
		copy(handlers, s.ContinuousHandlers)
	} else {
		handlers = append([]tjs.Value(nil), handlers...)
	}
	s.mu.RUnlock()

	trace := os.Getenv("KRKRGO_CONTINUOUS_TRACE") != ""
	args := [1]tjs.Value{tjs.IntegerValue(int64(tick))}
	for index, callback := range handlers {
		ref := callback.ObjectRef()
		if !continuousHandlerValid(vm, callback, ref) {
			s.mu.Lock()
			for i, existing := range s.ContinuousHandlers {
				if sameContinuousHandler(existing, callback) {
					s.ContinuousHandlers = append(s.ContinuousHandlers[:i], s.ContinuousHandlers[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
			continue
		}
		started := time.Time{}
		if trace {
			started = time.Now()
		}
		_, err := vm.CallFunction(callback, tjs.VoidValue, args[:], s, budget)
		if trace {
			ref := callback.ObjectRef()
			category := "unknown"
			name := ""
			if object, ok := vm.Objects.Lookup(ref.Object); ok && object != nil {
				category = fmt.Sprint(object.Category)
				if value, ok := object.Get("name"); ok {
					name = value.Text()
				}
			}
			log.Printf("continuous-handler index=%d function_id=%d context_id=%d category=%s name=%q duration=%s error=%v",
				index, ref.Object, ref.Context, category, name, time.Since(started), err)
		}
		if err != nil {
			s.mu.Lock()
			for i, existing := range s.ContinuousHandlers {
				if sameContinuousHandler(existing, callback) {
					s.ContinuousHandlers = append(s.ContinuousHandlers[:i], s.ContinuousHandlers[i+1:]...)
					break
				}
			}
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func systemBoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
