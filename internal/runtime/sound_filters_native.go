package runtime

import (
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const waveSoundFiltersObjectMember = "__krkrgoWaveFilters"

func (s *Services) waveSoundFilters(vm *tjs.VM, context tjs.Value, budget *uint64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer filters require VM")
	}
	if cached, found, err := vm.GetMemberIfPresent(context, waveSoundFiltersObjectMember, s, budget); err != nil {
		return tjs.VoidValue, err
	} else if found && cached.Kind() == tjs.KindObject && !cached.IsNull() {
		if valid, _ := vm.IsValid(cached).Int(); valid != 0 {
			if object, ok := vm.Objects.Lookup(cached.ObjectRef().Object); ok && object.Category == tjs.CategoryArray {
				return cached, nil
			}
		}
	}

	filters, err := vm.NewArray(nil)
	if err != nil {
		return tjs.VoidValue, err
	}
	if err := vm.SetMemberWithRuntime(context, waveSoundFiltersObjectMember, filters, tjs.HiddenMember, s, budget); err != nil {
		return tjs.VoidValue, err
	}
	return filters, nil
}
