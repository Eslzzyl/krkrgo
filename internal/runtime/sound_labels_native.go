package runtime

import (
	"errors"
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/assets"
	"github.com/eslzzyl/krkrgo/internal/tjs"
)

const waveSoundLabelsObjectMember = "__krkrgoWaveLabels"

func (s *Services) waveSoundLabels(vm *tjs.VM, context tjs.Value, sound *WaveSoundBuffer, budget *uint64) (tjs.Value, error) {
	if vm == nil {
		return tjs.VoidValue, fmt.Errorf("WaveSoundBuffer labels require VM")
	}
	if cached, found, err := vm.GetMemberIfPresent(context, waveSoundLabelsObjectMember, s, budget); err != nil {
		return tjs.VoidValue, err
	} else if found && cached.Kind() == tjs.KindObject && !cached.IsNull() {
		if valid, _ := vm.IsValid(cached).Int(); valid != 0 {
			return cached, nil
		}
	}

	labelsObject, err := vm.NewDictionary()
	if err != nil {
		return tjs.VoidValue, err
	}
	for _, label := range sound.Labels() {
		if budget != nil {
			if *budget == 0 {
				return tjs.VoidValue, ErrBudgetExceeded
			}
			*budget = *budget - 1
		}
		item, err := vm.NewDictionary()
		if err != nil {
			return tjs.VoidValue, err
		}
		positionMS := int64(0)
		if frequency := sound.Frequency(); frequency > 0 {
			positionMS = int64(label.Position * 1000 / uint64(frequency))
		}
		for _, member := range []struct {
			name  string
			value tjs.Value
		}{
			{name: "name", value: tjs.StringValue(label.Name)},
			{name: "samplePosition", value: tjs.IntegerValue(int64(label.Position))},
			{name: "position", value: tjs.IntegerValue(positionMS)},
		} {
			if err := vm.SetMemberWithRuntime(item, member.name, member.value, 0, s, budget); err != nil {
				return tjs.VoidValue, err
			}
		}
		if label.Name != "" {
			if err := vm.SetMemberWithRuntime(labelsObject, label.Name, item, 0, s, budget); err != nil {
				return tjs.VoidValue, err
			}
		}
	}
	if err := vm.SetMemberWithRuntime(context, waveSoundLabelsObjectMember, labelsObject, tjs.HiddenMember, s, budget); err != nil {
		return tjs.VoidValue, err
	}
	return labelsObject, nil
}

func (s *Services) invalidateWaveSoundLabels(vm *tjs.VM, context tjs.Value, budget *uint64) error {
	if vm == nil {
		return nil
	}
	cached, found, err := vm.GetMemberIfPresent(context, waveSoundLabelsObjectMember, s, budget)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if cached.Kind() == tjs.KindObject && !cached.IsNull() {
		if _, err := vm.Invalidate(cached, s, budget); err != nil {
			return err
		}
	}
	return vm.SetMemberWithRuntime(context, waveSoundLabelsObjectMember, tjs.NullValue, tjs.HiddenMember, s, budget)
}

func parseWaveSoundLoopInfo(s *Services, storage string) (assets.LoopInfo, error) {
	data, err := s.readStorage(storage + ".sli")
	if err != nil {
		if errors.Is(err, assets.ErrNotFound) {
			return assets.LoopInfo{}, nil
		}
		return assets.LoopInfo{}, err
	}
	text, err := assets.Decode(data)
	if err != nil {
		return assets.LoopInfo{}, fmt.Errorf("decode %s.sli: %w", storage, err)
	}
	info, err := assets.ParseSLI(text)
	if err != nil {
		return assets.LoopInfo{}, fmt.Errorf("parse %s.sli: %w", storage, err)
	}
	return *info, nil
}
