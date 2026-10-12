package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installAlphaMovieNative exposes the checked AMV playback implementation under
// the class name exported by AlphaMovie.dll. The plugin is linked lazily, as it
// is in TJS, so games which do not use AlphaMovie do not acquire the class.
func installAlphaMovieNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("AlphaMovie.dll requires a VM")
	}
	if _, installed := vm.Globals["AlphaMovie"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("AlphaMovie")
	if err != nil {
		return err
	}
	for _, method := range []string{"@initialize", "@invalidate", "AlphaMovie", "finalize", "open", "play", "stop", "close", "pause", "rewind", "setPos", "setSize", "setBounds", "prepare", "setSegmentLoop", "cancelSegmentLoop", "setPeriodEvent", "cancelPeriodEvent", "onStatusChanged", "onCallbackCommand", "onPeriod", "onFrameUpdate"} {
		if err := vm.RegisterNativeMethod(class, method, "AlphaMovie."+method); err != nil {
			return err
		}
	}
	for _, property := range []string{"position", "left", "top", "width", "height", "originalWidth", "originalHeight", "visible", "loop", "frame", "fps", "numberOfFrame", "totalTime", "layer1", "layer2", "mode", "playRate", "segmentLoopStartFrame", "segmentLoopEndFrame", "periodEventFrame", "audioBalance", "audioVolume", "numberOfAudioStream", "enabledAudioStream", "numberOfVideoStream", "enabledVideoStream", "mixingMovieAlpha", "mixingMovieBGColor"} {
		getter, setter := "get:"+property, "set:"+property
		switch property {
		case "originalWidth", "originalHeight", "fps", "numberOfFrame", "totalTime", "segmentLoopStartFrame", "segmentLoopEndFrame", "numberOfAudioStream", "numberOfVideoStream":
			setter = ""
		}
		if err := vm.RegisterNativeProperty(class, property, "AlphaMovie."+getter, "AlphaMovie."+setter); err != nil {
			return err
		}
	}
	return nil
}
