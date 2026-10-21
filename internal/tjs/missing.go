package tjs

func (v *VM) missingMember(receiver, key Value, input *Value, host Host, budget *uint64) (Value, bool, error) {
	if receiver.Kind() != KindObject || receiver.IsNull() {
		return VoidValue, false, nil
	}
	rootDepth := len(v.transientRoots)
	v.transientRoots = append(v.transientRoots, receiver, key)
	if input != nil {
		v.transientRoots = append(v.transientRoots, *input)
	}
	defer v.popTransientRoots(rootDepth)
	id, err := v.handle(receiver)
	if err != nil {
		return VoidValue, false, err
	}
	meta := v.meta[id]
	if meta == nil || !meta.callMissing || meta.processingMissing {
		return VoidValue, false, nil
	}
	if meta.category == CategoryArray {
		if _, indexed := arrayMemberIndex(key); indexed {
			return VoidValue, false, nil
		}
	}
	if _, exists, lookupErr := v.getMemberWithRuntime(id, key, host, budget); lookupErr != nil {
		return VoidValue, false, lookupErr
	} else if exists {
		return VoidValue, false, nil
	}

	meta.processingMissing = true
	defer func() {
		if current := v.meta[id]; current != nil {
			current.processingMissing = false
		}
	}()

	missing, exists, err := v.getMemberWithRuntime(id, StringValue("missing"), host, budget)
	if err != nil || !exists {
		return VoidValue, false, err
	}
	missing, err = v.getValue(receiver, missing, false, host, budget)
	if err != nil {
		return VoidValue, false, err
	}
	v.transientRoots = append(v.transientRoots, missing)
	if missing.Kind() != KindObject || missing.IsNull() {
		return VoidValue, false, nil
	}
	functionMeta := v.meta[missing.ObjectRef().Object]
	if functionMeta == nil || (functionMeta.category != CategoryFunction && functionMeta.category != CategoryNative && functionMeta.category != CategoryMethod) {
		return VoidValue, false, nil
	}

	cellValue := VoidValue
	if input != nil {
		cellValue = *input
	}
	_, cellID, err := v.alloc(CategoryProperty)
	if err != nil {
		return VoidValue, false, err
	}
	v.meta[cellID].variantProperty = &cellValue
	cell := ObjectValue(NewObjectRef(cellID))
	v.transientRoots = append(v.transientRoots, cell)
	setting := int64(0)
	if input != nil {
		setting = 1
	}
	result, err := v.invoke(missing, receiver, []Value{IntegerValue(setting), StringValue(key.Text()), cell}, host, budget, true)
	if err != nil {
		return VoidValue, false, err
	}
	handled, err := result.Integer()
	if err != nil || handled == 0 {
		return VoidValue, false, err
	}
	if input != nil {
		return VoidValue, true, nil
	}
	return *v.meta[cellID].variantProperty, true, nil
}
