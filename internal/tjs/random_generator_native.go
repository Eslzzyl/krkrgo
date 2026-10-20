package tjs

import (
	"fmt"
)

func (v *VM) randomGeneratorCall(context Value, operation string, args []Value, host Host, budget *uint64, resultNeeded bool) (Value, error) {
	id, err := v.handle(context)
	if err != nil {
		return VoidValue, err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != CategoryInstance || meta.class == nil || meta.class.Name != "RandomGenerator" {
		return VoidValue, fmt.Errorf("RandomGenerator.%s requires a RandomGenerator instance", operation)
	}
	switch operation {
	case "RandomGenerator", "randomize":
		generator := NewRandomGenerator()
		if len(args) != 0 {
			if err := v.randomizeGeneratorFromValue(generator, args[0], host, budget); err != nil {
				return VoidValue, err
			}
		}
		meta.randomGenerator = generator
		return VoidValue, nil
	case "@invalidate", "finalize":
		meta.randomGenerator = nil
		return VoidValue, nil
	}
	generator := meta.randomGenerator
	if generator == nil {
		return VoidValue, fmt.Errorf("RandomGenerator.%s: generator is not initialized", operation)
	}
	switch operation {
	case "random":
		return RealValue(generator.Random()), nil
	case "random32":
		return IntegerValue(int64(generator.Random32())), nil
	case "random63":
		return IntegerValue(generator.Random63()), nil
	case "random64":
		return IntegerValue(generator.Random64()), nil
	case "serialize":
		if !resultNeeded {
			return VoidValue, nil
		}
		state := generator.Serialize()
		value, err := v.NewDictionary()
		if err != nil {
			return VoidValue, err
		}
		id := value.ObjectRef().Object
		for name, member := range map[string]Value{
			"state": StringValue(state.State),
			"left":  IntegerValue(int64(state.Left)),
			"next":  IntegerValue(int64(state.Next)),
		} {
			if err := v.setMember(id, StringValue(name), member); err != nil {
				return VoidValue, err
			}
		}
		return value, nil
	default:
		return VoidValue, fmt.Errorf("unknown RandomGenerator operation: %s", operation)
	}
}

func (v *VM) randomizeGeneratorFromValue(generator *RandomGenerator, value Value, host Host, budget *uint64) error {
	if value.Kind() != KindObject {
		seed, err := value.Int()
		if err != nil {
			return err
		}
		return generator.Randomize(seed)
	}
	if value.IsNull() {
		return ErrNotReconstructiveRandomizeData
	}
	id, err := v.handle(value)
	if err != nil {
		return err
	}
	stateValue, stateOK, err := v.getMemberWithRuntime(id, StringValue("state"), host, budget)
	if err != nil {
		return err
	}
	leftValue, leftOK, err := v.getMemberWithRuntime(id, StringValue("left"), host, budget)
	if err != nil {
		return err
	}
	nextValue, nextOK, err := v.getMemberWithRuntime(id, StringValue("next"), host, budget)
	if err != nil {
		return err
	}
	if !stateOK || !leftOK || !nextOK {
		return ErrNotReconstructiveRandomizeData
	}
	left, err := leftValue.Int()
	if err != nil || left < 0 || left > mtN {
		return ErrNotReconstructiveRandomizeData
	}
	next, err := nextValue.Int()
	if err != nil || next < 0 || next > mtN {
		return ErrNotReconstructiveRandomizeData
	}
	return generator.Randomize(RandomGeneratorState{State: stateValue.Text(), Left: int(left), Next: int(next)})
}
