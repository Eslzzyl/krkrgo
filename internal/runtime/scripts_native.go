package runtime

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// registerScriptsNative installs the process-free subset of C++'s static
// Scripts class. The VM constructor hook intentionally rejects instances.
func (s *Session) registerScriptsNative() error {
	class, err := s.VM.RegisterNativeClass("Scripts")
	if err != nil {
		return err
	}
	if err := s.VM.RegisterNativeMethod(class, "@initialize", "Scripts.@initialize"); err != nil {
		return err
	}
	for _, method := range []string{"execStorage", "evalStorage", "compileStorage", "exec", "eval", "dump", "getTraceString", "setCallMissing", "getClassNames", "getObjectKeys", "getObjectCount", "getMD5HashString", "equalStruct", "equalStructNumericLoose"} {
		if err := s.VM.RegisterNativeStaticMethod(class, method, "Scripts."+method); err != nil {
			return err
		}
	}
	return s.VM.RegisterNativeStaticProperty(class, "textEncoding", "Scripts.get:textEncoding", "Scripts.set:textEncoding")
}

func (s *Services) scriptTextEncoding() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ScriptTextEncoding
}

func (s *Services) scriptTextEncodingConfig() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ScriptTextEncoding, s.scriptTextEncodingExplicit
}

func (s *Services) setScriptTextEncoding(encoding string) {
	s.mu.Lock()
	s.ScriptTextEncoding = encoding
	s.scriptTextEncodingExplicit = true
	s.mu.Unlock()
}

func (s *Services) decodeScriptStorage(data []byte) (string, error) {
	encoding, explicit := s.scriptTextEncodingConfig()
	if !explicit {
		return assets.Decode(data)
	}
	return decodeScriptText(data, encoding)
}

func (s *Services) scriptsNative(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64, resultNeeded bool) (tjs.Value, error) {
	arg := func(index int) (tjs.Value, error) {
		if index >= len(args) {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: missing argument %d", operation, index)
		}
		return args[index], nil
	}
	unsupported := func(detail string) (tjs.Value, error) {
		return tjs.VoidValue, fmt.Errorf("%w: Scripts.%s: %s", ErrUnsupportedNative, operation, detail)
	}
	finish := func(value tjs.Value, err error) (tjs.Value, error) {
		if err != nil {
			return tjs.VoidValue, err
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		return value, nil
	}
	preprocessor := s.preprocessorFor(vm)
	switch operation {
	case "@initialize":
		return tjs.VoidValue, fmt.Errorf("%w: Scripts is a static class", ErrUnsupportedNative)
	case "get:textEncoding":
		return tjs.StringValue(s.scriptTextEncoding()), nil
	case "set:textEncoding":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		s.setScriptTextEncoding(value.Text())
		return tjs.VoidValue, nil
	case "execStorage", "evalStorage":
		nameValue, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		storageName := nameValue.Text()
		mode := ""
		if len(args) >= 2 && !args[1].IsVoid() {
			mode = args[1].Text()
		}
		context := tjs.VoidValue
		if len(args) >= 3 && !args[2].IsVoid() {
			context = args[2]
		}
		profile := os.Getenv("KRKRGO_RENDER_STATS") != ""
		readStarted := time.Now()
		data, err := s.readStorage(storageName)
		readTime := time.Since(readStarted)
		if err != nil {
			return tjs.VoidValue, err
		}
		offset, err := scriptReadOffset(mode)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: %w", operation, err)
		}
		if offset > uint64(len(data)) {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: binary offset %d exceeds storage size %d", operation, offset, len(data))
		}
		data = data[int(offset):]
		if len(data) >= 4 && data[0] == 'T' && data[1] == 'J' && data[2] == 'S' && data[3] == '2' {
			return tjs.VoidValue, fmt.Errorf("%w: compiled TJS2 bytecode", ErrUnsupportedNative)
		}
		decodeStarted := time.Now()
		source, err := s.decodeScriptStorage(data)
		decodeTime := time.Since(decodeStarted)
		if err != nil {
			return tjs.VoidValue, err
		}
		if operation == "evalStorage" {
			source = stripEvalStorageConstDictionary(source)
		}
		executeStarted := time.Now()
		value, err := executeScriptSource(vm, s, budget, preprocessor, scriptStorageName(storageName), source, operation == "evalStorage", 0, context)
		executeTime := time.Since(executeStarted)
		if profile {
			log.Printf("krkrgo startup-script op=%s storage=%q bytes=%d read=%s decode=%s compile_execute=%s error=%t", operation, storageName, len(data), readTime, decodeTime, executeTime, err != nil)
		}
		return finish(value, err)
	case "exec", "eval":
		sourceValue, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		name := ""
		if len(args) >= 2 && !args[1].IsVoid() {
			name = args[1].Text()
		}
		var lineOffset int64
		if len(args) >= 3 && !args[2].IsVoid() {
			line, err := args[2].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			lineOffset = int64(int32(line))
		}
		context := tjs.VoidValue
		if len(args) >= 4 && !args[3].IsVoid() {
			context = args[3]
		}
		value, err := executeScriptSource(vm, s, budget, preprocessor, name, sourceValue.Text(), operation == "eval", lineOffset, context)
		return finish(value, err)
	case "getClassNames":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		classNames, err := vm.ClassNames(value)
		if err != nil {
			return tjs.VoidValue, err
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		items := make([]tjs.Value, len(classNames))
		for i, name := range classNames {
			items[i] = tjs.StringValue(name)
		}
		result, err := vm.NewArray(items)
		return finish(result, err)
	case "getObjectKeys", "getObjectCount", "getMD5HashString", "equalStruct", "equalStructNumericLoose":
		value, err := scriptsExNative(vm, operation, args, resultNeeded)
		return finish(value, err)
	case "compileStorage":
		if _, err := arg(0); err != nil {
			return tjs.VoidValue, err
		}
		if _, err := arg(1); err != nil {
			return tjs.VoidValue, err
		}
		if len(args) > 2 {
			end := len(args)
			if end > 5 {
				end = 5
			}
			for _, value := range args[2:end] {
				if !value.IsVoid() {
					if _, err := value.Int(); err != nil {
						return tjs.VoidValue, err
					}
				}
			}
		}
		return unsupported("compiled bytecode output is unavailable")
	case "dump":
		return unsupported("TJS heap dumping is unavailable")
	case "getTraceString":
		var limit int64
		if len(args) != 0 && !args[0].IsVoid() {
			var err error
			limit, err = args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
		}
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		return tjs.StringValue(vm.TraceString(limit)), nil
	case "setCallMissing":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, nil
		}
		return unsupported("TJS missing-member dispatch is unavailable")
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Scripts.%s", ErrUnsupportedNative, operation)
	}
}

func executeScriptSource(vm *tjs.VM, host tjs.Host, budget *uint64, preprocessor *tjs.Preprocessor, name, source string, expression bool, lineOffset int64, context tjs.Value) (tjs.Value, error) {
	program, err := tjs.CompileWithPreprocessor(name, source, expression, preprocessor)
	if err != nil {
		return tjs.VoidValue, err
	}
	if lineOffset != 0 {
		shiftProgramLines(&program, lineOffset)
	}
	if context.Kind() == tjs.KindObject && !context.IsNull() {
		return vm.ExecuteInContext(&program, context, host, budget)
	}
	return vm.Execute(&program, host, budget)
}

// stripEvalStorageConstDictionary removes the (const) annotations emitted by
// the C++ core serializer for nested constant dictionaries and arrays. The
// annotation is storage-format metadata, so it stays scoped to evalStorage
// rather than becoming general TJS expression syntax.
func stripEvalStorageConstDictionary(source string) string {
	const annotation = "(const)"
	var out strings.Builder
	out.Grow(len(source))
	var quote byte
	escaped := false
	lineComment := false
	blockComment := false
	for i := 0; i < len(source); {
		if quote != 0 {
			c := source[i]
			out.WriteByte(c)
			i++
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if lineComment {
			c := source[i]
			out.WriteByte(c)
			i++
			if c == '\n' || c == '\r' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if strings.HasPrefix(source[i:], "*/") {
				out.WriteString("*/")
				i += 2
				blockComment = false
			} else {
				out.WriteByte(source[i])
				i++
			}
			continue
		}
		if strings.HasPrefix(source[i:], "//") {
			out.WriteString("//")
			i += 2
			lineComment = true
			continue
		}
		if strings.HasPrefix(source[i:], "/*") {
			out.WriteString("/*")
			i += 2
			blockComment = true
			continue
		}
		if strings.HasPrefix(source[i:], annotation) {
			i += len(annotation)
			continue
		}
		c := source[i]
		out.WriteByte(c)
		i++
		if c == '\'' || c == '"' {
			quote = c
		}
	}
	return out.String()
}

func shiftProgramLines(program *tjs.Program, offset int64) {
	if program == nil || offset == 0 {
		return
	}
	for i := range program.Code {
		location := &program.Code[i].Location
		if offset > 0 {
			location.Line += uint64(offset)
		} else {
			delta := uint64(-offset)
			if location.Line < delta {
				location.Line = 0
			} else {
				location.Line -= delta
			}
		}
		op := &program.Code[i].Op
		shiftFunctionLines(op.Function, offset)
		shiftClassLines(op.Class, offset)
		shiftPropertyLines(op.Property, offset)
	}
}

func shiftFunctionLines(function *tjs.Function, offset int64) {
	if function != nil {
		shiftProgramLines(&function.Program, offset)
	}
}

func shiftPropertyLines(property *tjs.Property, offset int64) {
	if property != nil {
		shiftFunctionLines(property.Getter, offset)
		shiftFunctionLines(property.Setter, offset)
	}
}

func shiftClassLines(class *tjs.Class, offset int64) {
	if class == nil {
		return
	}
	shiftProgramLines(&class.Initializer, offset)
	for i := range class.Bases {
		shiftProgramLines(&class.Bases[i], offset)
	}
	for i := range class.Methods {
		shiftFunctionLines(&class.Methods[i], offset)
	}
	for i := range class.Properties {
		shiftPropertyLines(&class.Properties[i], offset)
	}
}

func scriptStorageName(name string) string {
	if index := strings.LastIndexAny(name, "/\\>"); index >= 0 {
		return name[index+1:]
	}
	return name
}

func scriptReadOffset(mode string) (uint64, error) {
	index := strings.IndexByte(mode, 'o')
	if index < 0 {
		return 0, nil
	}
	start := index + 1
	end := start
	for end < len(mode) && end-start < 255 && mode[end] >= '0' && mode[end] <= '9' {
		end++
	}
	if start == end {
		return 0, nil
	}
	offset, err := strconv.ParseUint(mode[start:end], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid binary offset in mode %q: %w", mode, err)
	}
	return offset, nil
}

func decodeScriptText(data []byte, encoding string) (string, error) {
	hasTextBOM := len(data) >= 3 && data[0] == 0xef && data[1] == 0xbb && data[2] == 0xbf
	if len(data) >= 2 && ((data[0] == 0xfe && data[1] == 0xfe) || (data[0] == 0xff && data[1] == 0xfe) || (data[0] == 0xfe && data[1] == 0xff)) || hasTextBOM {
		return assets.Decode(data)
	}
	switch encoding {
	case "UTF-8":
		if !utf8.Valid(data) {
			return "", fmt.Errorf("invalid UTF-8 script text")
		}
		return string(data), nil
	case "Shift_JIS":
		decoded, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), data)
		if err != nil {
			return "", fmt.Errorf("invalid Shift-JIS script text: %w", err)
		}
		text := string(decoded)
		for _, r := range text {
			if r == utf8.RuneError {
				return "", fmt.Errorf("invalid Shift-JIS script text: unmapped character")
			}
		}
		return text, nil
	default:
		return "", fmt.Errorf("unsupported script text encoding %q", encoding)
	}
}
