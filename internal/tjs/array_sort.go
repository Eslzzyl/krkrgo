package tjs

import (
	"sort"
	"strings"
)

// SortValues sorts a TJS array in place. mode is '+', '-', '0', '9', 'a', or
// 'z' (normal, reverse normal, numeric, reverse numeric, string, reverse
// string). The empty mode is equivalent to '+'. A custom comparator is used
// when non-nil and receives the same Value operands as TJS sort callbacks.
func SortValues(values []Value, mode string, stable bool, compare func(Value, Value) (bool, error)) error {
	if mode == "" {
		mode = "+"
	}
	mode = mode[:1]
	if mode != "+" && mode != "-" && mode != "0" && mode != "9" && mode != "a" && mode != "z" && compare == nil {
		return ErrType
	}
	var cmpErr error
	less := func(i, j int) bool {
		if cmpErr != nil {
			return false
		}
		a, b := values[i], values[j]
		if compare != nil {
			ok, err := compare(a, b)
			if err != nil {
				cmpErr = err
				return false
			}
			return ok
		}
		ascending := mode != "-" && mode != "9" && mode != "z"
		kind := mode
		if kind == "+" || kind == "-" {
			kind = "n"
		}
		var ok bool
		switch kind {
		case "0":
			ok = valueLessNumeric(a, b)
		case "a":
			ok = valueLessString(a, b)
		case "n":
			ok = valueLessNormal(a, b)
		default:
			ok = valueLessNormal(a, b)
		}
		if !ascending {
			return !ok && !valueEqualForSort(a, b, kind)
		}
		return ok
	}
	if stable {
		sort.SliceStable(values, less)
	} else {
		sort.Slice(values, less)
	}
	return cmpErr
}

func ArraySort(values []Value, mode string, stable bool, compare func(Value, Value) (bool, error)) error {
	return SortValues(values, mode, stable, compare)
}
func SortArray(values []Value, mode string, stable bool) error {
	return SortValues(values, mode, stable, nil)
}

func valueLessNormal(a, b Value) bool {
	if a.Kind() == KindString && b.Kind() == KindString {
		return compareUTF16(a.UTF16(), b.UTF16()) < 0
	}
	return valueLessNumeric(a, b)
}
func valueLessNumeric(a, b Value) bool {
	if a.Kind() == KindString && b.Kind() == KindString {
		na, _ := a.Number()
		nb, _ := b.Number()
		return valueNumericLess(na, nb)
	}
	na, ea := a.Number()
	nb, eb := b.Number()
	if ea != nil || eb != nil {
		return valueLessString(a, b)
	}
	return valueNumericLess(na, nb)
}
func valueNumericLess(a, b Value) bool {
	x, ex := a.Float()
	y, ey := b.Float()
	if ex != nil || ey != nil {
		return false
	}
	return x < y
}
func valueLessString(a, b Value) bool { return strings.Compare(a.Text(), b.Text()) < 0 }
func valueEqualForSort(a, b Value, kind string) bool {
	if kind == "a" || kind == "z" {
		return a.Text() == b.Text()
	}
	return a.Equal(b)
}
