package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installSampleNative attaches the optional getSample.dll surface to the
// existing WaveSoundBuffer native class only after Plugins.link activates it.
func installSampleNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("getSample.dll requires a VM")
	}
	class, ok := vm.Globals["WaveSoundBuffer"]
	if !ok || class.Kind() != tjs.KindObject {
		return fmt.Errorf("getSample.dll requires the WaveSoundBuffer native class")
	}
	for _, method := range []string{"setDefaultCounts", "setDefaultAheads"} {
		if err := vm.RegisterNativeStaticMethod(class, method, "WaveSoundBuffer."+method); err != nil {
			return err
		}
	}
	for _, property := range []struct{ name, getter, setter string }{
		{"sampleValue", "WaveSoundBuffer.get:sampleValue", ""},
		{"sampleCount", "WaveSoundBuffer.get:sampleCount", "WaveSoundBuffer.set:sampleCount"},
		{"sampleAhead", "WaveSoundBuffer.get:sampleAhead", "WaveSoundBuffer.set:sampleAhead"},
	} {
		if err := vm.RegisterNativeProperty(class, property.name, property.getter, property.setter); err != nil {
			return err
		}
	}
	return nil
}
