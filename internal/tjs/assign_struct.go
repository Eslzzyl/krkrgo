package tjs

import (
	"errors"
	"fmt"
)

type dictionaryAssignEntry struct {
	key   UTF16
	value Value
	flags uint32
}

func (v *VM) assignStructure(receiver Value, args []Value, array bool, budget *uint64) (Value, error) {
	targetID, err := v.handle(receiver)
	if err != nil {
		return VoidValue, err
	}
	if err := v.validID(targetID); err != nil {
		return VoidValue, err
	}
	target := v.meta[targetID]
	if target == nil || (array && target.category != CategoryArray) || (!array && target.category != CategoryDictionary) {
		return VoidValue, errors.New("assignStruct requires a matching native instance")
	}
	if len(args) == 0 {
		return VoidValue, ErrBadParamCount
	}
	if array {
		target.array = nil
		target.arrayLength = 0
	}
	sourceID, err := v.assignStructSourceID(args[0], target.category)
	if err != nil {
		return VoidValue, err
	}
	if err := v.copyStructureInto(targetID, sourceID, nil, budget); err != nil {
		return VoidValue, err
	}
	return VoidValue, nil
}

func (v *VM) assignStructSourceID(value Value, category ObjectCategory) (ObjectID, error) {
	if value.Kind() != KindObject || value.IsNull() {
		return 0, errors.New("assignStruct requires an Object source")
	}
	ref := value.ObjectRef()
	id := ref.Object
	if ref.HasBoundContext() {
		id = ref.Context
	}
	if err := v.validID(id); err != nil {
		return 0, err
	}
	meta := v.meta[id]
	if meta == nil || meta.category != category {
		return 0, errors.New("assignStruct requires a source of the same collection type")
	}
	return id, nil
}

func (v *VM) copyStructureInto(targetID, sourceID ObjectID, stack []ObjectID, budget *uint64) error {
	if len(stack) >= 128 {
		return vmAbortError(errors.New("assignStruct nesting limit exceeded"))
	}
	if err := chargeAssignmentBudget(budget, 1, "assignStruct"); err != nil {
		return err
	}
	targetObject, targetMeta, err := v.lookup(targetID)
	if err != nil {
		return err
	}
	sourceObject, sourceMeta, err := v.lookup(sourceID)
	if err != nil {
		return err
	}
	if targetMeta == nil || sourceMeta == nil || targetMeta.category != sourceMeta.category {
		return errors.New("assignStruct requires a source of the same collection type")
	}
	rootDepth := v.pushCallRoots(VoidValue, ObjectValue(NewObjectRef(targetID, targetID)), []Value{ObjectValue(NewObjectRef(sourceID, sourceID))})
	defer v.popTransientRoots(rootDepth)
	stack = append(stack, sourceID)

	if targetMeta.category == CategoryArray {
		targetMeta.array = nil
		targetMeta.arrayLength = 0
		for _, value := range sourceMeta.array {
			copied, err := v.copyStructureValue(value, stack, budget)
			if err != nil {
				return err
			}
			targetMeta.array = append(targetMeta.array, copied)
			targetMeta.arrayLength = int64(len(targetMeta.array))
		}
		return nil
	}
	if targetMeta.category != CategoryDictionary {
		return errors.New("assignStruct requires a matching native instance")
	}
	if err := clearDictionaryObject(targetObject); err != nil {
		return err
	}
	entries := sourceObject.Entries()
	for _, entry := range entries {
		if entry.Flags&MemberHidden != 0 {
			continue
		}
		copied, err := v.copyStructureValue(entry.Value, stack, budget)
		if err != nil {
			return err
		}
		if err := targetObject.SetUnits(entry.Key, copied); err != nil {
			return err
		}
	}
	return nil
}

func (v *VM) copyStructureValue(value Value, stack []ObjectID, budget *uint64) (Value, error) {
	if err := chargeAssignmentBudget(budget, 1, "assignStruct"); err != nil {
		return VoidValue, err
	}
	if value.Kind() != KindObject || value.IsNull() {
		return value, nil
	}
	id := value.ObjectRef().Object
	if id == 0 {
		return value, nil
	}
	object, ok := v.Objects.Lookup(id)
	if !ok || !object.IsValid() {
		return VoidValue, fmt.Errorf("invalid assignStruct object: %d", id)
	}
	meta := v.meta[id]
	if meta == nil || (meta.category != CategoryArray && meta.category != CategoryDictionary) {
		return value, nil
	}
	for _, sourceID := range stack {
		if sourceID == id {
			return NullValue, nil
		}
	}
	var copied Value
	var err error
	if meta.category == CategoryArray {
		copied, err = v.NewArray(nil)
	} else {
		copied, err = v.NewDictionary()
	}
	if err != nil {
		return VoidValue, err
	}
	if err := v.copyStructureInto(copied.ObjectRef().Object, id, stack, budget); err != nil {
		return VoidValue, err
	}
	return copied, nil
}

func (v *VM) dictionaryAssign(receiver Value, args []Value, budget *uint64) (Value, error) {
	targetID, err := v.handle(receiver)
	if err != nil {
		return VoidValue, err
	}
	if err := v.validID(targetID); err != nil {
		return VoidValue, err
	}
	targetMeta := v.meta[targetID]
	if targetMeta == nil || targetMeta.category != CategoryDictionary {
		return VoidValue, errors.New("Dictionary native instance required")
	}
	if len(args) == 0 {
		return VoidValue, ErrBadParamCount
	}
	clear := true
	if len(args) > 1 && !args[1].IsVoid() {
		value, err := args[1].Int()
		if err != nil {
			return VoidValue, err
		}
		clear = int32(value) != 0
	}
	source := args[0]
	if source.Kind() != KindObject || source.IsNull() {
		return VoidValue, errors.New("Dictionary.assign requires an Object source")
	}
	ref := source.ObjectRef()
	sourceID := ref.Object
	if ref.HasBoundContext() {
		sourceID = ref.Context
	}
	if err := v.validID(sourceID); err != nil {
		return VoidValue, err
	}
	targetObject, _, err := v.lookup(targetID)
	if err != nil {
		return VoidValue, err
	}
	if clear {
		if err := clearDictionaryObject(targetObject); err != nil {
			return VoidValue, err
		}
	}
	sourceObject, sourceMeta, err := v.lookup(sourceID)
	if err != nil {
		return VoidValue, err
	}
	if sourceMeta == nil {
		return VoidValue, errors.New("Dictionary.assign source enumeration is not implemented for this object type")
	}
	entries := make([]dictionaryAssignEntry, 0)
	reserve := 0
	switch sourceMeta.category {
	case CategoryArray:
		reserve = len(sourceMeta.array)
		for index := 0; index+1 < len(sourceMeta.array); index += 2 {
			key, err := sourceMeta.array[index].Unary("string")
			if err != nil {
				return VoidValue, err
			}
			entries = append(entries, dictionaryAssignEntry{key: key.UTF16(), value: sourceMeta.array[index+1]})
		}
	case CategoryDictionary, CategoryInstance, CategoryNamespace:
		for _, entry := range sourceObject.Entries() {
			if entry.Flags&MemberHidden != 0 {
				continue
			}
			entries = append(entries, dictionaryAssignEntry{key: entry.Key, value: entry.Value, flags: entry.Flags})
		}
		reserve = len(entries)
	default:
		return VoidValue, errors.New("Dictionary.assign source enumeration is not implemented for this object type")
	}
	if err := chargeAssignmentBudget(budget, uint64(len(entries)), "Dictionary.assign"); err != nil {
		return VoidValue, err
	}
	if targetObject.Len() > int(^uint(0)>>1)-reserve {
		return VoidValue, errors.New("Dictionary.assign member count overflow")
	}
	for _, entry := range entries {
		if err := setDictionaryMember(targetObject, entry.key, entry.value, entry.flags); err != nil {
			return VoidValue, err
		}
	}
	return VoidValue, nil
}
func (v *VM) dictionaryClear(receiver Value) (Value, error) {
	targetID, err := v.handle(receiver)
	if err != nil {
		return VoidValue, err
	}
	if err := v.validID(targetID); err != nil {
		return VoidValue, err
	}
	meta := v.meta[targetID]
	if meta == nil || meta.category != CategoryDictionary {
		return VoidValue, errors.New("Dictionary native instance required")
	}
	object, _, err := v.lookup(targetID)
	if err != nil {
		return VoidValue, err
	}
	if err := clearDictionaryObject(object); err != nil {
		return VoidValue, err
	}
	return VoidValue, nil
}

func clearDictionaryObject(object *Object) error {
	if object == nil {
		return errors.New("invalid TJS dictionary")
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if !object.valid {
		return errInvalidObject
	}
	clear(object.members)
	return nil
}

func setDictionaryMember(object *Object, key UTF16, value Value, flags uint32) error {
	if err := object.SetUnits(key, value); err != nil {
		return err
	}
	object.mu.Lock()
	defer object.mu.Unlock()
	if !object.valid {
		return errInvalidObject
	}
	k := memberKey(key)
	entry := object.members[k]
	entry.flags = flags
	entry.valid = true
	object.members[k] = entry
	return nil
}

func chargeAssignmentBudget(budget *uint64, amount uint64, operation string) error {
	if budget == nil {
		return nil
	}
	if amount > *budget {
		return vmAbortError(fmt.Errorf("%s execution budget exceeded", operation))
	}
	*budget -= amount
	return nil
}
