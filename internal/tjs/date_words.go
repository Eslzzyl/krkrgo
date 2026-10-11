package tjs

import "strings"

// DateWords contains the English names accepted by the TJS date parser.  TJS
// compares words case-insensitively and accepts both full and abbreviated
// month/day names.
var DateWords = map[string]int{
	"january": 0, "jan": 0, "february": 1, "feb": 1,
	"march": 2, "mar": 2, "april": 3, "apr": 3,
	"may": 4, "june": 5, "jun": 5, "july": 6, "jul": 6,
	"august": 7, "aug": 7, "september": 8, "sep": 8, "sept": 8,
	"october": 9, "oct": 9, "november": 10, "nov": 10,
	"december": 11, "dec": 11,
}

var dateWeekdays = map[string]timeWeekday{
	"sunday": 0, "sun": 0, "monday": 1, "mon": 1,
	"tuesday": 2, "tue": 2, "tues": 2, "wednesday": 3, "wed": 3,
	"thursday": 4, "thu": 4, "thur": 4, "thurs": 4,
	"friday": 5, "fri": 5, "saturday": 6, "sat": 6,
}

type timeWeekday int

// DateWordMonth returns the zero-based month number used by time.Time.
func DateWordMonth(word string) (int, bool) {
	m, ok := DateWords[strings.ToLower(strings.TrimSpace(word))]
	return m, ok
}

// DateWordWeekday reports whether word is a recognized weekday. Weekdays are
// syntactic in TJS and are not used to validate the numerical date.
func DateWordWeekday(word string) (int, bool) {
	w, ok := dateWeekdays[strings.ToLower(strings.TrimSpace(word))]
	return int(w), ok
}

var dateZones = map[string]int{
	"ut": 0, "utc": 0, "gmt": 0, "z": 0,
	"est": -5 * 60, "edt": -4 * 60, "cst": -6 * 60, "cdt": -5 * 60,
	"mst": -7 * 60, "mdt": -6 * 60, "pst": -8 * 60, "pdt": -7 * 60,
}

func dateZoneMinutes(word string) (int, bool) {
	m, ok := dateZones[strings.ToLower(strings.TrimSpace(word))]
	return m, ok
}

// DateWordZone returns a fixed offset in minutes east of UTC for a common
// English zone abbreviation.
func DateWordZone(word string) (int, bool) { return dateZoneMinutes(word) }
