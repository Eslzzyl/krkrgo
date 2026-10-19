package runtime

import (
	"strings"
	"time"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const maxDebugLogLines = 2148

func (s *Services) debugLoggingNative(vm *tjs.VM, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	switch operation {
	case "addLoggingHandler", "removeLoggingHandler":
		if len(args) == 0 {
			return tjs.VoidValue, tjs.ErrBadParamCount
		}
		handler := args[0]
		s.mu.Lock()
		defer s.mu.Unlock()
		if operation == "addLoggingHandler" {
			for _, registered := range s.debugLogHandlers {
				if registered.StrictEqual(handler) {
					return tjs.VoidValue, nil
				}
			}
			s.debugLogHandlers = append(s.debugLogHandlers, handler)
			return tjs.VoidValue, nil
		}
		for index, registered := range s.debugLogHandlers {
			if registered.StrictEqual(handler) {
				s.debugLogHandlers = append(s.debugLogHandlers[:index], s.debugLogHandlers[index+1:]...)
				break
			}
		}
		return tjs.VoidValue, nil
	case "getLastLog":
		lines := maxDebugLogLines
		if len(args) != 0 {
			value, err := args[0].Int()
			if err != nil {
				return tjs.VoidValue, err
			}
			if value < 0 {
				lines = maxDebugLogLines
			} else if value < int64(lines) {
				lines = int(value)
			}
		}
		s.mu.RLock()
		start := len(s.debugLogLines) - lines
		if start < 0 {
			start = 0
		}
		text := strings.Join(s.debugLogLines[start:], "")
		s.mu.RUnlock()
		return tjs.StringValue(text), nil
	default:
		return tjs.VoidValue, tjs.ErrNotImplemented
	}
}

func (s *Services) appendDebugLog(vm *tjs.VM, message string, budget *uint64) error {
	if s == nil {
		return nil
	}
	s.Messages = append(s.Messages, message)
	line := time.UnixMilli(s.UnixTimeMS()).Local().Format("15:04:05") + " " + message + "\n"
	s.mu.Lock()
	s.debugLogLines = append(s.debugLogLines, line)
	if len(s.debugLogLines) > maxDebugLogLines {
		copy(s.debugLogLines, s.debugLogLines[len(s.debugLogLines)-maxDebugLogLines:])
		s.debugLogLines = s.debugLogLines[:maxDebugLogLines]
	}
	if s.deliveringDebugLog {
		s.mu.Unlock()
		return nil
	}
	s.deliveringDebugLog = true
	handlers := append([]tjs.Value(nil), s.debugLogHandlers...)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.deliveringDebugLog = false
		s.mu.Unlock()
	}()
	if vm == nil {
		return nil
	}
	for _, handler := range handlers {
		s.mu.RLock()
		registered := false
		for _, current := range s.debugLogHandlers {
			if current.StrictEqual(handler) {
				registered = true
				break
			}
		}
		s.mu.RUnlock()
		if !registered {
			continue
		}
		if _, err := vm.CallFunction(handler, tjs.VoidValue, []tjs.Value{tjs.StringValue(line)}, s, budget); err != nil {
			s.removeDebugLogHandler(handler)
		}
	}
	return nil
}

func (s *Services) removeDebugLogHandler(handler tjs.Value) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index, registered := range s.debugLogHandlers {
		if registered.StrictEqual(handler) {
			s.debugLogHandlers = append(s.debugLogHandlers[:index], s.debugLogHandlers[index+1:]...)
			return
		}
	}
}
