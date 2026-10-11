package tjs

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrCannotParseDate          = errors.New("TJS cannot parse date")
	ErrInvalidValueTimestamp    = errors.New("TJS invalid value for timestamp")
	ErrInvalidValueForTimestamp = ErrInvalidValueTimestamp
	ErrInvalidTimestamp         = ErrInvalidValueTimestamp
)

// Date is a TJS Date value. The stored value is milliseconds since the Unix
// epoch, exactly as getTime/setTime expose it; calendar getters use the local
// time zone, like the C++ implementation's localtime calls.
type Date struct {
	millis int64
	valid  bool
}

type TJSDate = Date

// NewDate constructs a Date. With no arguments it uses the current instant.
// A string is parsed by ParseDateString. Numeric arguments are year, month
// (zero based), day, hour, minute and second, with the same defaults and
// overflow normalization as time.Date/mktime.
func NewDate(args ...any) *Date {
	d := &Date{valid: true}
	if len(args) == 0 {
		d.millis = time.Now().UnixMilli()
		return d
	}
	if len(args) == 1 {
		switch v := args[0].(type) {
		case string:
			ms, err := ParseDateString(v)
			if err != nil {
				return &Date{valid: false}
			}
			d.millis = ms
			return d
		case *Date:
			if v == nil || !v.valid {
				return &Date{valid: false}
			}
			d.millis = v.millis
			return d
		case Value:
			if v.Kind() == KindString {
				ms, err := ParseDateString(v.Text())
				if err != nil {
					return &Date{valid: false}
				}
				d.millis = ms
				return d
			}
			n, err := v.Int()
			if err != nil {
				return &Date{valid: false}
			}
			d.millis = time.Date(int(n), 1, 1, 0, 0, 0, 0, time.Local).UnixMilli()
			return d
		case time.Time:
			d.millis = v.UnixMilli()
			return d
		}
	}
	vals := make([]int, 6)
	defaults := [...]int{0, 0, 1, 0, 0, 0}
	for i := range vals {
		vals[i] = defaults[i]
	}
	for i, a := range args {
		if i >= len(vals) {
			break
		}
		n, ok := dateInt(a)
		if !ok {
			return &Date{valid: false}
		}
		vals[i] = n
	}
	// TJS months are zero based. time.Date intentionally normalizes values
	// outside their ordinary ranges, as mktime does.
	t := time.Date(vals[0], time.Month(vals[1])+1, vals[2], vals[3], vals[4], vals[5], 0, time.Local)
	d.millis = t.UnixMilli()
	return d
}

// NewDateChecked is the error-reporting form of NewDate.
func NewDateChecked(args ...any) (*Date, error) {
	if len(args) == 1 {
		if s, ok := args[0].(string); ok {
			return ParseDate(s)
		}
	}
	d := NewDate(args...)
	if !d.valid {
		return nil, ErrInvalidValueTimestamp
	}
	return d, nil
}

func dateInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), uint64(n) <= uint64(^uint(0)>>1)
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), uint64(n) <= uint64(^uint(0)>>1)
	case float64:
		if n != n {
			return 0, false
		}
		return int(n), true
	case float32:
		if n != n {
			return 0, false
		}
		return int(n), true
	case Value:
		i, err := n.Int()
		return int(i), err == nil && i >= int64(-int(^uint(0)>>1)-1) && i <= int64(^uint(0)>>1)
	default:
		return 0, false
	}
}

func NewDateFromTime(t time.Time) *Date   { return &Date{millis: t.UnixMilli(), valid: true} }
func DateFromMilliseconds(ms int64) *Date { return &Date{millis: ms, valid: true} }
func NewDateMilliseconds(ms int64) *Date  { return DateFromMilliseconds(ms) }

func ParseDate(s string) (*Date, error) {
	ms, err := ParseDateString(s)
	if err != nil {
		return nil, err
	}
	return DateFromMilliseconds(ms), nil
}

var dateNumeric = regexp.MustCompile(`^\s*(\d{1,6})[-/.](\d{1,2})[-/.](\d{1,6})(?:[ T]+(\d{1,2}):(\d{1,2})(?::(\d{1,2})(?:[.,](\d+))?)?)?\s*(.*)\s*$`)
var dateTimeOnly = regexp.MustCompile(`^\s*(\d{1,6})[-/.](\d{1,2})[-/.](\d{1,6})[ T]+(\d{1,2}):(\d{1,2})(?::(\d{1,2})(?:[.,](\d+))?)?\s*(.*)\s*$`)

// ParseDateString parses the date forms accepted by TJS: numeric ISO-like
// forms, RFC-822 forms, English month names, and explicit UTC offsets. An
// absent zone means local time. Years 00..50 map to 2000..2050 and 51..99
// map to 1951..1999, matching tTJSDateParser::SetYear.
func ParseDateString(input string) (int64, error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return 0, ErrCannotParseDate
	}
	if t, err := time.Parse(time.RFC3339Nano, strings.Replace(s, " ", "T", 1)); err == nil {
		return t.UnixMilli(), nil
	}
	if t, err := time.Parse(time.RFC1123Z, s); err == nil {
		return t.UnixMilli(), nil
	}
	if ms, err := parseNamedDate(s); err == nil {
		return ms, nil
	}
	if t, err := time.Parse(time.RFC850, s); err == nil {
		return t.UnixMilli(), nil
	}
	if m := dateNumeric.FindStringSubmatch(s); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if y < 100 {
			y = dateExpandYear(y)
		}
		hour, minute, sec := 0, 0, 0
		if m[4] != "" {
			hour, _ = strconv.Atoi(m[4])
			minute, _ = strconv.Atoi(m[5])
			if m[6] != "" {
				sec, _ = strconv.Atoi(m[6])
			}
		}
		nsec, ok := dateFraction(m[7])
		if !ok {
			return 0, ErrCannotParseDate
		}
		return dateBuild(y, mo-1, day, hour, minute, sec, nsec, m[8])
	}
	return parseNamedDate(s)
}

func dateExpandYear(y int) int {
	if y <= 50 {
		return 2000 + y
	}
	return 1900 + y
}
func dateFraction(s string) (int, bool) {
	if s == "" {
		return 0, true
	}
	if len(s) > 9 {
		s = s[:9]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	for i := len(s); i < 9; i++ {
		n *= 10
	}
	return n, true
}

func dateBuild(year, month, day, hour, minute, sec, nsec int, zone string) (int64, error) {
	loc := time.Local
	zone = strings.TrimSpace(zone)
	if zone != "" {
		if strings.HasPrefix(zone, "Z") || strings.HasPrefix(zone, "z") {
			loc = time.UTC
			zone = strings.TrimSpace(zone[1:])
		} else if mins, ok := parseNumericZone(zone); ok {
			loc = time.FixedZone("TJS", mins*60)
			zone = ""
		} else if mins, ok := dateZoneMinutes(strings.Fields(zone)[0]); ok {
			loc = time.FixedZone("TJS", mins*60)
			zone = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(zone, strings.Fields(zone)[0]), ","))
		}
	}
	if zone != "" {
		return 0, ErrCannotParseDate
	}
	t := time.Date(year, time.Month(month+1), day, hour, minute, sec, nsec, loc)
	return t.UnixMilli(), nil
}

func parseNumericZone(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	z := strings.TrimPrefix(strings.TrimPrefix(s, "+"), "-")
	z = strings.TrimSpace(z)
	var h, m int
	var err error
	if strings.Contains(z, ":") {
		var parts []string
		parts = strings.SplitN(z, ":", 2)
		if len(parts) != 2 {
			return 0, false
		}
		h, err = strconv.Atoi(parts[0])
		if err != nil {
			return 0, false
		}
		m, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, false
		}
	} else if len(z) == 4 {
		h, err = strconv.Atoi(z[:2])
		if err != nil {
			return 0, false
		}
		m, err = strconv.Atoi(z[2:])
		if err != nil {
			return 0, false
		}
	} else {
		return 0, false
	}
	if h > 23 || m > 59 {
		return 0, false
	}
	mins := h*60 + m
	if s[0] == '-' {
		mins = -mins
	}
	return mins, true
}

func parseNamedDate(s string) (int64, error) {
	fields := strings.Fields(strings.Trim(s, ","))
	if len(fields) > 0 {
		if _, ok := DateWordWeekday(strings.TrimSuffix(fields[0], ",")); ok {
			fields = fields[1:]
		}
	}
	if len(fields) < 3 {
		return 0, ErrCannotParseDate
	}
	// Accept month-day-year and day-month-year. The parser also tolerates a
	// comma after the day, as in RFC-822.
	var day, year, month int
	var ok bool
	if month, ok = DateWordMonth(strings.Trim(fields[0], ",")); ok {
		day, _ = strconv.Atoi(strings.Trim(fields[1], ","))
		year, _ = strconv.Atoi(strings.Trim(fields[2], ","))
	} else {
		day, _ = strconv.Atoi(strings.Trim(fields[0], ","))
		month, ok = DateWordMonth(strings.Trim(fields[1], ","))
		year, _ = strconv.Atoi(strings.Trim(fields[2], ","))
	}
	if !ok || year == 0 {
		return 0, ErrCannotParseDate
	}
	if year < 100 {
		year = dateExpandYear(year)
	}
	hour, minute, sec, nsec := 0, 0, 0, 0
	zone := ""
	if len(fields) >= 4 {
		p := strings.Split(fields[3], ":")
		if len(p) < 2 || len(p) > 3 {
			return 0, ErrCannotParseDate
		}
		hour, _ = strconv.Atoi(p[0])
		minute, _ = strconv.Atoi(p[1])
		if len(p) == 3 {
			sec, _ = strconv.Atoi(p[2])
		}
	}
	if len(fields) >= 5 {
		zone = strings.Join(fields[4:], " ")
	}
	return dateBuild(year, month, day, hour, minute, sec, nsec, zone)
}

func (d Date) IsValid() bool { return d.valid }
func (d Date) GetTime() int64 {
	if !d.valid {
		return 0
	}
	return d.millis
}
func (d Date) UnixMilli() int64       { return d.GetTime() }
func (d Date) Time() time.Time        { return time.UnixMilli(d.GetTime()).UTC() }
func (d Date) local() time.Time       { return time.UnixMilli(d.GetTime()).In(time.Local) }
func (d Date) GetYear() int           { return d.local().Year() }
func (d Date) GetMonth() int          { return int(d.local().Month()) - 1 }
func (d Date) GetDate() int           { return d.local().Day() }
func (d Date) GetDay() int            { return int(d.local().Weekday()) }
func (d Date) GetHours() int          { return d.local().Hour() }
func (d Date) GetMinutes() int        { return d.local().Minute() }
func (d Date) GetSeconds() int        { return d.local().Second() }
func (d Date) GetTimezoneOffset() int { _, off := d.local().Zone(); return -off / 60 }
func DateTimezoneOffset() int         { _, off := time.Now().Zone(); return -off / 60 }

func (d *Date) setLocal(f func(time.Time) time.Time) error {
	if d == nil || !d.valid {
		return ErrInvalidValueTimestamp
	}
	d.millis = f(d.local()).UnixMilli()
	return nil
}
func (d *Date) SetYear(y int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(y, t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetMonth(m int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(t.Year(), time.Month(m+1), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetDate(day int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetHours(h int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), h, t.Minute(), t.Second(), t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetMinutes(m int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), m, t.Second(), t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetSeconds(s int) error {
	return d.setLocal(func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), s, t.Nanosecond(), time.Local)
	})
}
func (d *Date) SetTime(ms int64) error {
	if d == nil {
		return ErrInvalidValueTimestamp
	}
	d.millis, d.valid = ms, true
	return nil
}
func (d *Date) Parse(s string) error {
	ms, err := ParseDateString(s)
	if err != nil {
		return err
	}
	return d.SetTime(ms)
}

// Format returns the fixed-width local calendar form used in TJS diagnostics.
func (d Date) Format() string {
	if !d.valid {
		return "Invalid Date"
	}
	return d.local().Format("2006-01-02 15:04:05")
}
func (d Date) FormatUTC() string {
	if !d.valid {
		return "Invalid Date"
	}
	return d.Time().Format("2006-01-02 15:04:05")
}
func (d Date) ToString() string              { return d.Format() }
func FormatDateMilliseconds(ms int64) string { return DateFromMilliseconds(ms).Format() }
func (d Date) String() string                { return d.Format() }
func (d Date) GoString() string              { return fmt.Sprintf("Date(%q)", d.Format()) }
