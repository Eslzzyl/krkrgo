package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func installKirikiroid2Native(vm *tjs.VM) error {
	if vm == nil {
		return fmt.Errorf("nil TJS VM")
	}
	storages, ok := vm.Globals["Storages"]
	if !ok {
		return fmt.Errorf("Storages native class is unavailable")
	}
	return vm.RegisterNativeStaticMethod(storages, "setTextEncoding", "Storages.setTextEncoding")
}
