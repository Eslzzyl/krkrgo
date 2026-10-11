package runtime

import (
	"fmt"
	"strings"

	"github.com/eslzzyl/krkrgo/internal/tjs"
)

func (s *Services) mathNative(operation string, args []tjs.Value, resultNeeded bool) (tjs.Value, error) {
	if strings.HasPrefix(operation, "get:") {
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		var value float64
		switch strings.TrimPrefix(operation, "get:") {
		case "E":
			value = tjs.MathE
		case "LOG2E":
			value = tjs.MathLog2E
		case "LOG10E":
			value = tjs.MathLog10E
		case "LN10":
			value = tjs.MathLn10
		case "LN2":
			value = tjs.MathLn2
		case "PI":
			value = tjs.MathPI
		case "SQRT1_2":
			value = tjs.MathSqrt1_2
		case "SQRT2":
			value = tjs.MathSqrt2
		default:
			return tjs.VoidValue, fmt.Errorf("%w: Math.%s", ErrUnsupportedNative, operation)
		}
		return tjs.RealValue(value), nil
	}
	required := 1
	switch operation {
	case "atan2", "pow":
		required = 2
	case "max", "min", "random":
		required = 0
	default:
		switch operation {
		case "abs", "acos", "asin", "atan", "ceil", "cos", "exp", "floor", "log", "round", "sin", "sqrt", "tan":
		default:
			return tjs.VoidValue, fmt.Errorf("%w: Math.%s", ErrUnsupportedNative, operation)
		}
	}
	if len(args) < required {
		return tjs.VoidValue, tjs.ErrBadParamCount
	}
	if !resultNeeded && operation != "random" {
		return tjs.VoidValue, nil
	}
	if operation == "random" {
		if !resultNeeded {
			return tjs.VoidValue, nil
		}
		return tjs.RealValue(tjs.MathRandom()), nil
	}
	if operation == "max" || operation == "min" {
		value := tjs.MathMin()
		if operation == "max" {
			value = tjs.MathMax()
		}
		for _, arg := range args {
			number, err := arg.Float()
			if err != nil {
				return tjs.VoidValue, err
			}
			if operation == "max" {
				value = tjs.TJSMath.Max(value, number)
			} else {
				value = tjs.TJSMath.Min(value, number)
			}
		}
		return tjs.RealValue(value), nil
	}
	first, err := args[0].Float()
	if err != nil {
		return tjs.VoidValue, err
	}
	var second float64
	if required == 2 {
		second, err = args[1].Float()
		if err != nil {
			return tjs.VoidValue, err
		}
	}
	math := tjs.TJSMath
	var value float64
	switch operation {
	case "abs":
		value = math.Abs(first)
	case "acos":
		value = math.Acos(first)
	case "asin":
		value = math.Asin(first)
	case "atan":
		value = math.Atan(first)
	case "atan2":
		value = math.Atan2(first, second)
	case "ceil":
		value = math.Ceil(first)
	case "cos":
		value = math.Cos(first)
	case "exp":
		value = math.Exp(first)
	case "floor":
		value = math.Floor(first)
	case "log":
		value = math.Log(first)
	case "pow":
		value = math.Pow(first, second)
	case "round":
		value = math.Round(first)
	case "sin":
		value = math.Sin(first)
	case "sqrt":
		value = math.Sqrt(first)
	case "tan":
		value = math.Tan(first)
	}
	return tjs.RealValue(value), nil
}
