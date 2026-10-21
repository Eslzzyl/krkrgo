package runtime

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type csvParserState struct {
	target    tjs.Value
	separator rune
	newline   string
	lines     []string
	line      int
	records   int64
}

func (s *Services) csvParserNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if s.CSVParsers == nil {
		s.CSVParsers = make(map[tjs.ObjectID]*csvParserState)
	}
	id := context.ObjectRef().Object
	state := s.CSVParsers[id]
	if operation == "@initialize" {
		if s.csvParserVM != vm {
			return tjs.VoidValue, fmt.Errorf("CSVParser.dll must be linked before constructing CSVParser")
		}
		separator, newline := ',', "\r\n"
		if len(args) > 1 {
			n, err := args[1].Int()
			if err != nil {
				return tjs.VoidValue, fmt.Errorf("CSVParser separator: %w", err)
			}
			separator = rune(n)
		}
		if len(args) > 2 {
			newline = args[2].Text()
		}
		target := context
		if len(args) > 0 && args[0].Kind() == tjs.KindObject && !args[0].IsNull() {
			target = args[0]
		}
		s.CSVParsers[id] = &csvParserState{target: target, separator: separator, newline: newline}
		return tjs.VoidValue, nil
	}
	if operation == "CSVParser" {
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		delete(s.CSVParsers, id)
		return tjs.VoidValue, nil
	}
	if state == nil {
		return tjs.VoidValue, fmt.Errorf("CSVParser.%s called before initialization", operation)
	}
	load := func(text string) { state.lines = splitCSVPhysicalLines(text); state.line = 0; state.records = 0 }
	switch operation {
	case "init":
		if len(args) == 0 {
			return tjs.VoidValue, tjs.ErrBadParamCount
		}
		load(args[0].Text())
		return tjs.VoidValue, nil
	case "initStorage":
		if len(args) == 0 {
			return tjs.VoidValue, tjs.ErrBadParamCount
		}
		data, err := s.readStorage(args[0].Text())
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("CSVParser.initStorage %q: %w", args[0].Text(), err)
		}
		text, err := assets.Decode(data)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("CSVParser.initStorage %q: %w", args[0].Text(), err)
		}
		if len(args) > 1 {
			truth, e := args[1].Truth()
			if e != nil {
				return tjs.VoidValue, e
			}
			if truth {
				text = string(data)
			}
		}
		load(text)
		return tjs.VoidValue, nil
	case "getNextLine":
		row, ok, err := state.next(vm)
		if err != nil {
			return tjs.VoidValue, err
		}
		if !ok {
			return tjs.VoidValue, nil
		}
		return row, nil
	case "parse", "parseStorage":
		if len(args) > 0 {
			if operation == "parse" {
				load(args[0].Text())
			} else {
				data, err := s.readStorage(args[0].Text())
				if err != nil {
					return tjs.VoidValue, fmt.Errorf("CSVParser.parseStorage %q: %w", args[0].Text(), err)
				}
				text, err := assets.Decode(data)
				if err != nil {
					return tjs.VoidValue, fmt.Errorf("CSVParser.parseStorage %q: %w", args[0].Text(), err)
				}
				if len(args) > 1 {
					truth, e := args[1].Truth()
					if e != nil {
						return tjs.VoidValue, e
					}
					if truth {
						text = string(data)
					}
				}
				load(text)
			}
		}
		if state.target.Kind() != tjs.KindObject || !tjs.NewDispatcher(vm.Objects).Has(state.target, "doLine") {
			return tjs.VoidValue, nil
		}
		method, err := vm.GetMember(state.target, "doLine", s, budget)
		if err != nil {
			return tjs.VoidValue, fmt.Errorf("CSVParser.%s target.doLine: %w", operation, err)
		}
		for {
			row, ok, e := state.next(vm)
			if e != nil {
				return tjs.VoidValue, e
			}
			if !ok {
				break
			}
			// The reference plugin invokes doLine and ignores its returned HRESULT.
			_, _ = vm.CallFunction(method, state.target, []tjs.Value{row, tjs.IntegerValue(state.records)}, s, budget)
		}
		return tjs.VoidValue, nil
	case "get:currentLineNumber":
		return tjs.IntegerValue(state.records), nil
	default:
		return tjs.VoidValue, fmt.Errorf("unknown CSVParser operation: %s", operation)
	}
}

func splitCSVPhysicalLines(text string) []string {
	if text == "" {
		return nil
	}
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func (p *csvParserState) next(vm *tjs.VM) (tjs.Value, bool, error) {
	if p.line >= len(p.lines) {
		return tjs.VoidValue, false, nil
	}
	var record strings.Builder
	for {
		if p.line >= len(p.lines) {
			break
		}
		if record.Len() > 0 {
			record.WriteString(p.newline)
		}
		line := p.lines[p.line]
		p.line++
		record.WriteString(line)
		if csvRecordComplete(record.String(), p.separator) {
			break
		}
	}
	var fields []string
	if record.Len() != 0 {
		reader := csv.NewReader(strings.NewReader(record.String()))
		reader.Comma = p.separator
		reader.LazyQuotes = true
		reader.FieldsPerRecord = -1
		var err error
		fields, err = reader.Read()
		if err != nil && err != io.EOF {
			return tjs.VoidValue, false, fmt.Errorf("CSVParser record %d: %w", p.records+1, err)
		}
	}
	items := make([]tjs.Value, len(fields))
	for i, field := range fields {
		items[i] = tjs.StringValue(field)
	}
	row, err := vm.NewArray(items)
	if err != nil {
		return tjs.VoidValue, false, err
	}
	p.records++
	return row, true, nil
}

func csvRecordComplete(text string, separator rune) bool {
	quoted, fieldStart := false, true
	runes := []rune(text)
	skipNext := false
	for i, r := range runes {
		if skipNext {
			skipNext = false
			continue
		}
		if quoted {
			if r == '"' {
				if i+1 < len(runes) && runes[i+1] == '"' {
					skipNext = true
					continue
				}
				quoted = false
			}
			continue
		}
		if fieldStart && r == '"' {
			quoted = true
			fieldStart = false
		} else if r == separator {
			fieldStart = true
		} else {
			fieldStart = false
		}
	}
	return !quoted
}
