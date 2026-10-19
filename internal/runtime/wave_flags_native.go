package runtime

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const (
	waveSoundFlagsObjectMember = "__krkrgoWaveFlags"
	waveFlagsBufferIDMember    = "__krkrgoBufferID"
)

func (s *Services) waveFlagsNative(vm *tjs.VM, context tjs.Value, operation string, args []tjs.Value, budget *uint64) (tjs.Value, error) {
	if operation == "get:count" {
		return tjs.IntegerValue(16), nil
	}
	if operation == "@initialize" {
		if vm == nil || context.Kind() != tjs.KindObject || context.IsNull() {
			return tjs.VoidValue, fmt.Errorf("WaveFlags requires an object context")
		}
		if len(args) == 0 || args[0].Kind() != tjs.KindObject || args[0].IsNull() {
			return tjs.VoidValue, fmt.Errorf("WaveFlags requires a WaveSoundBuffer")
		}
		bufferID := args[0].ObjectRef().Object
		s.mu.RLock()
		sound := s.Sounds[bufferID]
		s.mu.RUnlock()
		if sound == nil {
			return tjs.VoidValue, fmt.Errorf("WaveFlags buffer has no native sound instance")
		}
		return tjs.VoidValue, vm.SetMemberWithRuntime(
			context,
			waveFlagsBufferIDMember,
			tjs.IntegerValue(int64(bufferID)),
			tjs.HiddenMember,
			s,
			budget,
		)
	}
	if operation == "@invalidate" || operation == "WaveFlags" || operation == "finalize" {
		return tjs.VoidValue, nil
	}
	if operation == "reset" {
		sound, err := s.waveFlagsSound(vm, context, budget)
		if err != nil {
			return tjs.VoidValue, err
		}
		if sound != nil {
			if stream := sound.Stream(); stream != nil {
				stream.ResetFlags()
			}
		}
		return tjs.VoidValue, nil
	}

	var index int
	var err error
	if strings.HasPrefix(operation, "get:") {
		index, err = strconv.Atoi(strings.TrimPrefix(operation, "get:"))
	} else if strings.HasPrefix(operation, "set:") {
		index, err = strconv.Atoi(strings.TrimPrefix(operation, "set:"))
	} else {
		return tjs.VoidValue, fmt.Errorf("unknown WaveFlags operation: %s", operation)
	}
	if err != nil || index < 0 || index >= 16 {
		return tjs.VoidValue, fmt.Errorf("invalid WaveFlags index in operation %q", operation)
	}
	sound, err := s.waveFlagsSound(vm, context, budget)
	if err != nil {
		return tjs.VoidValue, err
	}
	if sound == nil || sound.Stream() == nil {
		if strings.HasPrefix(operation, "get:") {
			return tjs.IntegerValue(0), nil
		}
		return tjs.VoidValue, nil
	}
	stream := sound.Stream()
	if strings.HasPrefix(operation, "get:") {
		value, err := stream.Flag(index)
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(int64(value)), nil
	}
	if len(args) == 0 {
		return tjs.VoidValue, fmt.Errorf("WaveFlags.%d: missing argument 0", index)
	}
	value, err := args[0].Int()
	if err != nil {
		return tjs.VoidValue, err
	}
	return tjs.VoidValue, stream.SetFlag(index, int32(value))
}

func (s *Services) waveFlagsSound(vm *tjs.VM, context tjs.Value, budget *uint64) (*WaveSoundBuffer, error) {
	if vm == nil || context.Kind() != tjs.KindObject || context.IsNull() {
		return nil, fmt.Errorf("WaveFlags requires an object context")
	}
	bufferIDValue, found, err := vm.GetMemberIfPresent(context, waveFlagsBufferIDMember, s, budget)
	if err != nil {
		return nil, err
	}
	if !found || bufferIDValue.Kind() != tjs.KindInteger {
		return nil, fmt.Errorf("WaveFlags has no WaveSoundBuffer")
	}
	bufferID, err := bufferIDValue.Int()
	if err != nil || bufferID < 0 {
		return nil, fmt.Errorf("WaveFlags has an invalid WaveSoundBuffer")
	}
	s.mu.RLock()
	sound := s.Sounds[tjs.ObjectID(bufferID)]
	s.mu.RUnlock()
	return sound, nil
}
