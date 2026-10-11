package tjs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var errInvalidFormatString = errors.New("TJS invalid format string")

// FormatString formats TJS values using the conversion subset supported by
// C++ TJSFormatString.
func FormatString(format string, params ...Value) (string, error) {
	result, err := formatTJSString(StringValue(format), params)
	if err != nil {
		return "", err
	}
	return result.Text(), nil
}

// formatTJSString implements the format subset supported by C++ TJSFormatString.
// The format and string precision are measured in UTF-16 code units, as in TJS.
func formatTJSString(format Value, args []Value) (Value, error) {
	units := format.string
	out := make([]uint16, 0, len(units))
	arg := 0
	for i := 0; i < len(units); {
		if units[i] != '%' {
			out = append(out, units[i])
			i++
			continue
		}
		i++
		if i == len(units) {
			return Value{}, errInvalidFormatString
		}
		flags := ""
		if units[i] == '-' || units[i] == '+' || units[i] == '#' {
			flags = string(rune(units[i]))
			i++
			if i == len(units) {
				return Value{}, errInvalidFormatString
			}
		}
		zero := false
		if units[i] == '0' {
			zero = true
			i++
			if i == len(units) {
				return Value{}, errInvalidFormatString
			}
		}
		width, widthSet, next, err := parseFormatNumber(units, i)
		if err != nil {
			return Value{}, err
		}
		i = next
		widthStar := false
		if i < len(units) && units[i] == '*' {
			widthStar = true
			i++
		}
		precision, precisionSet, precisionStar := 0, false, false
		if i < len(units) && units[i] == '.' {
			i++
			if i == len(units) {
				return Value{}, errInvalidFormatString
			}
			if units[i] == '*' {
				precisionStar = true
				i++
			} else if units[i] >= '0' && units[i] <= '9' {
				precision, _, i, err = parseFormatNumber(units, i)
				if err != nil {
					return Value{}, err
				}
				precisionSet = true
			} else {
				return Value{}, errInvalidFormatString
			}
		}
		if i >= len(units) {
			return Value{}, errInvalidFormatString
		}
		typ := units[i]
		i++
		if typ == '%' {
			out = append(out, '%')
			continue
		}
		if !strings.ContainsRune("diouxXfegEGcs", rune(typ)) {
			return Value{}, errInvalidFormatString
		}
		if widthStar {
			width, err = formatIntArg(args, &arg)
			if err != nil {
				return Value{}, err
			}
			if width < 0 {
				if !strings.Contains(flags, "-") {
					flags += "-"
				}
				width = -width
			}
			widthSet = true
		}
		if precisionStar {
			precision, err = formatIntArg(args, &arg)
			if err != nil {
				return Value{}, err
			}
			precisionSet = precision >= 0
		}
		if width > 900 || precision > 900 || width+precision > 900 {
			return Value{}, errInvalidFormatString
		}
		if typ == 's' || typ == 'c' {
			if arg >= len(args) {
				return Value{}, ErrBadParamCount
			}
			text := args[arg].Text()
			arg++
			value := UTF16FromString(text)
			count := len(value)
			if precisionSet && precision != 0 && precision < count {
				count = precision
			}
			if typ == 'c' && count > 1 {
				count = 1
			}
			pad := width - count
			if pad < 0 {
				pad = 0
			}
			if !strings.Contains(flags, "-") {
				out = appendSpaces(out, pad)
			}
			out = append(out, value[:count]...)
			if strings.Contains(flags, "-") {
				out = appendSpaces(out, pad)
			}
			continue
		}
		if arg >= len(args) {
			return Value{}, ErrBadParamCount
		}
		v := args[arg]
		arg++
		spec := "%" + flags
		if zero {
			spec += "0"
		}
		if widthSet {
			spec += strconv.Itoa(width)
		}
		if precisionSet {
			spec += "." + strconv.Itoa(precision)
		}
		switch typ {
		case 'd', 'i':
			n, e := v.Int()
			if e != nil {
				return Value{}, e
			}
			spec += "d"
			out = append(out, UTF16FromString(fmt.Sprintf(spec, n))...)
		case 'o':
			n, e := v.Int()
			if e != nil {
				return Value{}, e
			}
			spec += "o"
			out = append(out, UTF16FromString(fmt.Sprintf(spec, uint64(n)))...)
		case 'u':
			n, e := v.Int()
			if e != nil {
				return Value{}, e
			}
			spec += "d"
			out = append(out, UTF16FromString(fmt.Sprintf(spec, uint64(n)))...)
		case 'x', 'X':
			n, e := v.Int()
			if e != nil {
				return Value{}, e
			}
			spec += string(rune(typ))
			out = append(out, UTF16FromString(fmt.Sprintf(spec, uint64(n)))...)
		default:
			n, e := v.Float()
			if e != nil {
				return Value{}, e
			}
			spec += string(rune(typ))
			out = append(out, UTF16FromString(fmt.Sprintf(spec, n))...)
		}
	}
	return StringUnits(out), nil
}

func parseFormatNumber(units []uint16, i int) (value int, set bool, next int, err error) {
	next = i
	for next < len(units) && units[next] >= '0' && units[next] <= '9' {
		set = true
		digit := int(units[next] - '0')
		if value > 900 || value*10+digit > 900 {
			return 0, false, next, errInvalidFormatString
		}
		value = value*10 + digit
		next++
	}
	return
}

func formatIntArg(args []Value, arg *int) (int, error) {
	if *arg >= len(args) {
		return 0, ErrBadParamCount
	}
	n, err := args[*arg].Int()
	*arg = *arg + 1
	if err != nil {
		return 0, err
	}
	if n > 900 || n < -900 {
		return 0, errInvalidFormatString
	}
	return int(n), nil
}

func appendSpaces(out []uint16, n int) []uint16 {
	for ; n > 0; n-- {
		out = append(out, ' ')
	}
	return out
}
