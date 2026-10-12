package runtime

import (
	"fmt"
	"math"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

type phaseVocoderConfig struct {
	window  int
	overlap int
	pitch   float32
	time    float32
}

func defaultPhaseVocoderConfig() *phaseVocoderConfig {
	return &phaseVocoderConfig{window: 4096, pitch: 1, time: 1}
}

// PhaseVocoderNative implements Kirikiri's framework-provided nested native
// class and its portable configuration surface. Audio filter processing is not
// enabled by this headless runtime.
func (s *Services) PhaseVocoderNative(context tjs.Value, operation string, args []tjs.Value) (tjs.Value, error) {
	s.ensureInit()
	if context.Kind() != tjs.KindObject || context.IsNull() {
		return tjs.VoidValue, fmt.Errorf("PhaseVocoder requires object context")
	}
	id := context.ObjectRef().Object
	if operation == "@initialize" {
		s.mu.Lock()
		s.PhaseVocoders[id] = defaultPhaseVocoderConfig()
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	if operation == "@invalidate" || operation == "finalize" {
		s.mu.Lock()
		delete(s.PhaseVocoders, id)
		s.mu.Unlock()
		return tjs.VoidValue, nil
	}
	s.mu.RLock()
	config := s.PhaseVocoders[id]
	s.mu.RUnlock()
	if config == nil {
		return tjs.VoidValue, fmt.Errorf("context has no PhaseVocoder native instance")
	}
	switch operation {
	case "PhaseVocoder":
		return tjs.VoidValue, nil
	case "get:interface":
		return tjs.IntegerValue(int64(id) + 1), nil
	case "get:window":
		return tjs.IntegerValue(int64(config.window)), nil
	case "get:overlap":
		return tjs.IntegerValue(int64(config.overlap)), nil
	case "get:pitch":
		return tjs.RealValue(float64(config.pitch)), nil
	case "get:time":
		return tjs.RealValue(float64(config.time)), nil
	}
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("PhaseVocoder setter requires a value")
	}
	switch operation {
	case "set:window":
		value, err := args[0].Int()
		if err != nil || value < 64 || value > 32768 || value&(value-1) != 0 {
			return tjs.VoidValue, fmt.Errorf("invalid vocoder window: expected a power of two from 64 to 32768")
		}
		config.window = int(value)
	case "set:overlap":
		value, err := args[0].Int()
		if err != nil || (value != 0 && value != 2 && value != 4 && value != 8 && value != 16 && value != 32) {
			return tjs.VoidValue, fmt.Errorf("invalid vocoder overlap: expected 0, 2, 4, 8, 16 or 32")
		}
		config.overlap = int(value)
	case "set:pitch", "set:time":
		value, err := args[0].Real()
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return tjs.VoidValue, fmt.Errorf("PhaseVocoder %s requires a finite number", operation[4:])
		}
		if operation == "set:pitch" {
			config.pitch = float32(value)
		} else {
			config.time = float32(value)
		}
	default:
		return tjs.VoidValue, fmt.Errorf("unknown PhaseVocoder operation: %s", operation)
	}
	return tjs.VoidValue, nil
}
