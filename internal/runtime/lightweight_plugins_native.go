package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// installGetAboutNative confirms the session-registered getabout.dll export.
// The method is available for the lifetime of a Session; linking records plugin
// compatibility but does not dynamically extend the System namespace.
func installGetAboutNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("getabout.dll requires a VM")
	}
	system, ok := vm.Globals["System"]
	if !ok {
		return fmt.Errorf("getabout.dll requires the System namespace")
	}
	if _, err := vm.GetMember(system, "getAboutString", nil, nil); err != nil {
		return fmt.Errorf("getabout.dll requires the session System.getAboutString method: %w", err)
	}
	return nil
}

// installWin32DialogNative evaluates the C++ plugin's WIN32Dialog script class
// using the existing host dialog service. System.inform returns void in TJS,
// hence !System.inform(...) evaluates to true after a successful dialog.
func installWin32DialogNative(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("win32dialog.dll requires a VM")
	}
	if _, installed := vm.Globals["WIN32Dialog"]; installed {
		return nil
	}
	class, err := vm.RegisterNativeClass("WIN32Dialog")
	if err != nil {
		return err
	}
	return vm.RegisterNativeMethod(class, "messageBox", "WIN32Dialog.messageBox")
}

func (s *Services) win32DialogNative(operation string, args []tjs.Value) (tjs.Value, error) {
	if operation == "construct" {
		return tjs.VoidValue, nil
	}
	if operation != "messageBox" {
		return tjs.VoidValue, fmt.Errorf("unknown WIN32Dialog operation %q", operation)
	}
	if len(args) > 3 {
		return tjs.VoidValue, fmt.Errorf("WIN32Dialog.messageBox accepts message, caption, and type")
	}
	message, caption := "", ""
	if len(args) > 0 && !args[0].IsVoid() {
		message = args[0].Text()
	}
	if len(args) > 1 && !args[1].IsVoid() {
		caption = args[1].Text()
	}
	if s.Dialogs == nil {
		return tjs.VoidValue, ErrDialogUnavailable
	}
	if _, err := s.Dialogs.ShowMessage(caption, message); err != nil {
		return tjs.VoidValue, err
	}
	return tjs.IntegerValue(1), nil
}

// installFFTGraphNative installs the C++ plugin's drawFFTGraph global but
// reports the missing renderer contract explicitly rather than claiming a draw.
func installFFTGraphNative(vm *tjs.VM, services *Services) error {
	if vm == nil || services == nil {
		return fmt.Errorf("fftgraph.dll requires a VM and services")
	}
	if _, installed := vm.Globals["drawFFTGraph"]; installed {
		return nil
	}
	if _, ok := services.Natives.Entry("drawFFTGraph"); !ok {
		if err := services.Natives.RegisterFunc("drawFFTGraph", func(_ tjs.Value, _ []tjs.Value) (tjs.Value, error) {
			return tjs.VoidValue, UnsupportedDevice("fftgraph renderer")
		}); err != nil {
			return err
		}
	}
	return vm.RegisterNative("drawFFTGraph")
}
