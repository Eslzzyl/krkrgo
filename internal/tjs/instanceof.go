package tjs

import "errors"

// instanceOf implements the TJS instanceof operator. The right operand is
// converted to text by the VM before this method is called, matching TJS's
// class-name based operator rather than JavaScript's prototype-based form.
func (v *VM) instanceOf(value Value, name string) (Value, error) {
	if value.Kind() == KindVoid {
		return boolValue(false), nil
	}
	if name == "Object" {
		return boolValue(true), nil
	}
	switch value.Kind() {
	case KindInteger, KindReal:
		return boolValue(name == "Number"), nil
	case KindString:
		return boolValue(name == "String"), nil
	case KindOctet:
		return boolValue(name == "Octet"), nil
	case KindObject:
		id := value.ObjectRef().Object
		if id == 0 {
			return boolValue(false), nil
		}
		object, metadata, err := v.lookup(id)
		if err != nil {
			return VoidValue, err
		}
		if !object.IsValid() {
			return VoidValue, errors.New("TJS object is invalid")
		}

		category := object.Category
		if metadata != nil {
			category = metadata.category
		}
		if category == CategoryInstance {
			if metadata != nil && (classHasName(metadata.class, name) || v.classObjectHasName(metadata.classObject, name, 0)) {
				return boolValue(true), nil
			}
			if class := classForObject(id); class != nil && classMetaHasName(class, name, make(map[*classMeta]struct{})) {
				return boolValue(true), nil
			}
		}
		switch category {
		case CategoryArray:
			return boolValue(name == "Array"), nil
		case CategoryDictionary:
			return boolValue(name == "Dictionary"), nil
		case CategoryClass:
			return boolValue(name == "Class"), nil
		case CategoryProperty:
			return boolValue(name == "Property"), nil
		case CategoryFunction, CategoryNative, CategoryMethod:
			if category == CategoryNative && regexesHas(id) {
				return boolValue(name == "RegExp"), nil
			}
			return boolValue(name == "Function"), nil
		}
	}
	return boolValue(false), nil
}

func classHasName(class *Class, name string) bool {
	if class == nil {
		return false
	}
	if class.Name == name {
		return true
	}
	for _, base := range class.Bases {
		if base.Storage == name {
			return true
		}
		if baseMeta := classFromProgram(base); baseMeta != nil && classMetaHasName(baseMeta, name, make(map[*classMeta]struct{})) {
			return true
		}
	}
	return false
}

func classMetaHasName(class *classMeta, name string, seen map[*classMeta]struct{}) bool {
	if class == nil {
		return false
	}
	if _, ok := seen[class]; ok {
		return false
	}
	seen[class] = struct{}{}
	if class.definition.Name == name {
		return true
	}
	for _, base := range class.bases {
		if classMetaHasName(base, name, seen) {
			return true
		}
	}
	return false
}
func (v *VM) instanceOfValue(value, classValue Value) (Value, error) {
	if classValue.Kind() == KindObject && !classValue.IsNull() {
		id := classValue.ObjectRef().Object
		object, metadata, err := v.lookup(id)
		if err != nil {
			return VoidValue, err
		}
		if object.Category == CategoryClass && metadata != nil && metadata.class != nil {
			return v.instanceOf(value, metadata.class.Name)
		}
	}
	return v.instanceOf(value, classValue.Text())
}
