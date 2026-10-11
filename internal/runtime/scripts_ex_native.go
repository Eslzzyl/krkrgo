package runtime

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

// scriptsExNative implements the process-independent ScriptsEx helpers exposed
// by the official plugin. Operations here inspect only ordinary VM values.
func scriptsExNative(vm *tjs.VM, operation string, args []tjs.Value, resultNeeded bool) (tjs.Value, error) {
	arg := func(i int) (tjs.Value, error) {
		if i >= len(args) {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: missing argument %d", operation, i)
		}
		return args[i], nil
	}
	switch operation {
	case "getObjectKeys", "getObjectCount":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindObject || value.IsNull() {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: expected object", operation)
		}
		object, ok := vm.Objects.Lookup(value.ObjectRef().Object)
		if !ok {
			return tjs.VoidValue, fmt.Errorf("Scripts.%s: invalid object", operation)
		}
		if operation == "getObjectCount" {
			return tjs.IntegerValue(int64(object.Len())), nil
		}
		keys := object.Keys()
		items := make([]tjs.Value, len(keys))
		for i, key := range keys {
			items[i] = tjs.StringValue(key)
		}
		return vm.NewArray(items)
	case "getMD5HashString":
		value, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		if value.Kind() != tjs.KindOctet {
			return tjs.VoidValue, fmt.Errorf("Scripts.getMD5HashString: expected Octet")
		}
		digest := md5.Sum(value.Octets())
		return tjs.StringValue(hex.EncodeToString(digest[:])), nil
	case "equalStruct", "equalStructNumericLoose":
		left, err := arg(0)
		if err != nil {
			return tjs.VoidValue, err
		}
		right, err := arg(1)
		if err != nil {
			return tjs.VoidValue, err
		}
		equal, err := scriptsExEqual(vm, left, right, operation == "equalStructNumericLoose", make(map[[2]tjs.ObjectID]bool))
		if err != nil {
			return tjs.VoidValue, err
		}
		return tjs.IntegerValue(map[bool]int64{false: 0, true: 1}[equal]), nil
	default:
		return tjs.VoidValue, fmt.Errorf("%w: Scripts.%s", ErrUnsupportedNative, operation)
	}
}

func scriptsExEqual(vm *tjs.VM, left, right tjs.Value, loose bool, seen map[[2]tjs.ObjectID]bool) (bool, error) {
	if loose && (left.Kind() == tjs.KindInteger || left.Kind() == tjs.KindReal) && (right.Kind() == tjs.KindInteger || right.Kind() == tjs.KindReal) {
		l, err := left.Float()
		if err != nil {
			return false, err
		}
		r, err := right.Float()
		if err != nil {
			return false, err
		}
		return l == r || math.IsNaN(l) && math.IsNaN(r), nil
	}
	if left.Kind() != right.Kind() {
		return false, nil
	}
	if left.Kind() != tjs.KindObject || left.IsNull() || right.IsNull() {
		switch left.Kind() {
		case tjs.KindVoid:
			return true, nil
		case tjs.KindInteger:
			return left.IntegerValue() == right.IntegerValue(), nil
		case tjs.KindReal:
			return left.RealValue() == right.RealValue(), nil
		case tjs.KindString:
			return bytes.Equal([]byte(left.Text()), []byte(right.Text())), nil
		case tjs.KindOctet:
			return bytes.Equal(left.Octets(), right.Octets()), nil
		default:
			return left.IsNull() && right.IsNull(), nil
		}
	}
	lid, rid := left.ObjectRef().Object, right.ObjectRef().Object
	if lid == rid {
		return true, nil
	}
	lo, lok := vm.Objects.Lookup(lid)
	r, rok := vm.Objects.Lookup(rid)
	if !lok || !rok {
		return false, fmt.Errorf("Scripts.equalStruct: invalid object")
	}
	if lo.Category != tjs.CategoryArray && lo.Category != tjs.CategoryDictionary {
		return false, nil
	}
	if lo.Category != r.Category || lo.Len() != r.Len() {
		return false, nil
	}
	pair := [2]tjs.ObjectID{lid, rid}
	if seen[pair] {
		return true, nil
	}
	seen[pair] = true
	defer delete(seen, pair)
	leftEntries, rightEntries := lo.Entries(), r.Entries()
	for i, entry := range leftEntries {
		var rightValue tjs.Value
		if lo.Category == tjs.CategoryDictionary {
			var found bool
			rightValue, found = r.Get(entry.Name)
			if !found {
				return false, nil
			}
		} else {
			if entry.Name != rightEntries[i].Name {
				return false, nil
			}
			rightValue = rightEntries[i].Value
		}
		equal, err := scriptsExEqual(vm, entry.Value, rightValue, loose, seen)
		if err != nil || !equal {
			return equal, err
		}
	}
	return true, nil
}
