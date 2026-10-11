package assets

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrInvalidSLI identifies malformed or unsupported SLI input.
var ErrInvalidSLI = errors.New("invalid SLI")

// Condition controls a conditional loop link.
type Condition uint8

const (
	ConditionNone Condition = iota
	ConditionEqual
	ConditionNotEqual
	ConditionGreater
	ConditionGreaterOrEqual
	ConditionLess
	ConditionLessOrEqual
)

// Link maps one audio frame position to another.
type Link struct {
	From      uint64    `json:"from"`
	To        uint64    `json:"to"`
	Smooth    bool      `json:"smooth"`
	Condition Condition `json:"condition"`
	Reference int32     `json:"reference"`
	// Variable is -1 for an unconditional link, or one of 16 sound flags.
	Variable int32 `json:"variable"`
}

// Matches reports whether the link applies to flags.
func (l Link) Matches(flags [16]int32) bool {
	if l.Variable == -1 {
		return true
	}
	if l.Variable < 0 || l.Variable >= int32(len(flags)) {
		return false
	}
	flag := flags[l.Variable]
	switch l.Condition {
	case ConditionNone:
		return true
	case ConditionEqual:
		return flag == l.Reference
	case ConditionNotEqual:
		return flag != l.Reference
	case ConditionGreater:
		return flag > l.Reference
	case ConditionGreaterOrEqual:
		return flag >= l.Reference
	case ConditionLess:
		return flag < l.Reference
	case ConditionLessOrEqual:
		return flag <= l.Reference
	default:
		return false
	}
}

// Label names a sample-frame position.
type Label struct {
	Position uint64 `json:"position"`
	Name     string `json:"name"`
}

// LoopInfo is SLI loop and label metadata.
type LoopInfo struct {
	Links  []Link  `json:"links"`
	Labels []Label `json:"labels"`
}

func sliErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSLI, fmt.Sprintf(format, args...))
}

// ParseSLI parses legacy loop metadata or version 2.00 SLI text.
func ParseSLI(source string) (*LoopInfo, error) {
	if len(source) > 4<<20 {
		return nil, sliErr("text exceeds limit")
	}
	if !utf8.ValidString(source) {
		return nil, sliErr("invalid UTF-8 text")
	}
	if strings.IndexByte(source, 0) >= 0 {
		return nil, sliErr("NUL in text")
	}
	info := &LoopInfo{}
	if !strings.HasPrefix(source, "#") {
		loopStart, err := sliLegacyField(source, "LoopStart=")
		if err != nil {
			return nil, err
		}
		length, err := sliLegacyField(source, "LoopLength=")
		if err != nil {
			return nil, err
		}
		if loopStart > ^uint64(0)-length {
			return nil, sliErr("loop endpoint overflow")
		}
		info.Links = append(info.Links, Link{From: loopStart + length, To: loopStart})
		return info, nil
	}
	first := source
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimRight(first, "\r")
	if first != "#2.00" {
		return nil, sliErr("unsupported version")
	}
	parser := sliParser{source: source}
	for parser.skip() {
		if len(info.Links)+len(info.Labels) >= 65_536 {
			return nil, sliErr("event limit exceeded")
		}
		kind, err := parser.word()
		if err != nil {
			return nil, err
		}
		if err := parser.expect('{'); err != nil {
			return nil, err
		}
		fields := make(map[string]string)
		for {
			parser.skip()
			if parser.eat('}') {
				break
			}
			key, err := parser.word()
			if err != nil {
				return nil, err
			}
			if err := parser.expect('='); err != nil {
				return nil, err
			}
			parser.skip()
			quoted := parser.eat('\'')
			start := parser.pos
			for {
				c := parser.peek()
				if c == 0 || (quoted && c == '\'') || (!quoted && (isASCIIWhitespace(c) || c == ';')) {
					break
				}
				parser.pos += utf8.RuneLen(c)
			}
			value := parser.source[start:parser.pos]
			if quoted && !parser.eat('\'') {
				return nil, sliErr("unterminated value")
			}
			if err := parser.expect(';'); err != nil {
				return nil, err
			}
			key = strings.ToLower(key)
			if _, exists := fields[key]; exists {
				return nil, sliErr("duplicate field %s", key)
			}
			fields[key] = value
		}
		kind = strings.ToLower(kind)
		switch kind {
		case "link":
			link := Link{}
			for key, value := range fields {
				switch key {
				case "from":
					link.From, err = strconv.ParseUint(value, 10, 64)
					if err != nil {
						return nil, sliErr("invalid From")
					}
				case "to":
					link.To, err = strconv.ParseUint(value, 10, 64)
					if err != nil {
						return nil, sliErr("invalid To")
					}
				case "smooth":
					switch strings.ToLower(value) {
					case "true", "yes":
						link.Smooth = true
					case "false", "no":
						link.Smooth = false
					default:
						return nil, sliErr("invalid Smooth")
					}
				case "condition":
					link.Condition, err = parseCondition(value)
					if err != nil {
						return nil, err
					}
				case "refvalue":
					v, parseErr := strconv.ParseInt(value, 10, 32)
					if parseErr != nil {
						return nil, sliErr("invalid RefValue")
					}
					link.Reference = int32(v)
				case "condvar":
					v, parseErr := strconv.ParseInt(value, 10, 32)
					if parseErr != nil {
						return nil, sliErr("invalid CondVar")
					}
					link.Variable = int32(v)
				default:
					return nil, sliErr("unsupported link field %s", key)
				}
			}
			if link.Variable < -1 || link.Variable >= 16 {
				return nil, sliErr("condition flag index out of range")
			}
			info.Links = append(info.Links, link)
		case "label":
			label := Label{}
			for key, value := range fields {
				switch key {
				case "position":
					var parseErr error
					label.Position, parseErr = strconv.ParseUint(value, 10, 64)
					if parseErr != nil {
						return nil, sliErr("invalid Position")
					}
				case "name":
					label.Name = value
				default:
					return nil, sliErr("unsupported label field %s", key)
				}
			}
			info.Labels = append(info.Labels, label)
		default:
			return nil, sliErr("unsupported entity %s", kind)
		}
	}
	info.SortLinks()
	return info, nil
}

func sliLegacyField(source, key string) (uint64, error) {
	_, tail, ok := strings.Cut(source, key)
	if !ok {
		return 0, sliErr("missing legacy field")
	}
	tail = strings.TrimLeftFunc(tail, unicodeASCIIWhitespace)
	end := 0
	for end < len(tail) && tail[end] >= '0' && tail[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, sliErr("invalid legacy integer")
	}
	value, err := strconv.ParseUint(tail[:end], 10, 64)
	if err != nil {
		return 0, sliErr("invalid legacy integer")
	}
	return value, nil
}

func parseCondition(value string) (Condition, error) {
	switch strings.ToLower(value) {
	case "no":
		return ConditionNone, nil
	case "eq":
		return ConditionEqual, nil
	case "ne":
		return ConditionNotEqual, nil
	case "gt":
		return ConditionGreater, nil
	case "ge":
		return ConditionGreaterOrEqual, nil
	case "lt":
		return ConditionLess, nil
	case "le":
		return ConditionLessOrEqual, nil
	default:
		return ConditionNone, sliErr("invalid Condition")
	}
}

// Sort orders conditional links before unconditional links at a frame, and
// labels by position.
func (i *LoopInfo) Sort() {
	i.SortLinks()
	sort.SliceStable(i.Labels, func(a, b int) bool { return i.Labels[a].Position < i.Labels[b].Position })
}

// SortLinks orders links for NextLink. Labels retain file order.
func (i *LoopInfo) SortLinks() {
	sort.SliceStable(i.Links, func(a, b int) bool {
		x, y := i.Links[a], i.Links[b]
		if x.From != y.From {
			return x.From < y.From
		}
		if x.Condition != y.Condition {
			return x.Condition > y.Condition
		}
		return x.Variable > y.Variable
	})
}

// ValidateFrames checks links, labels, condition indexes, and event count.
func (i *LoopInfo) ValidateFrames(frames uint64) error {
	if len(i.Links)+len(i.Labels) > 65_536 {
		return sliErr("event limit exceeded")
	}
	for _, link := range i.Links {
		if link.From > frames || link.To > frames {
			return sliErr("link is outside audio frames")
		}
		if link.Variable < -1 || link.Variable >= 16 {
			return sliErr("condition flag index out of range")
		}
	}
	for _, label := range i.Labels {
		if label.Position > frames {
			return sliErr("label is outside audio frames")
		}
	}
	return nil
}

// NextLink returns the first matching link at or after position. Call Sort
// after changing Links.
func (i *LoopInfo) NextLink(position uint64, flags *[16]int32) *Link {
	start := sort.Search(len(i.Links), func(n int) bool { return i.Links[n].From >= position })
	for n := start; n < len(i.Links); n++ {
		if flags == nil || i.Links[n].Matches(*flags) {
			return &i.Links[n]
		}
	}
	return nil
}

type sliParser struct {
	source string
	pos    int
}

func (p *sliParser) peek() rune {
	if p.pos >= len(p.source) {
		return 0
	}
	c, _ := utf8.DecodeRuneInString(p.source[p.pos:])
	return c
}

func (p *sliParser) eat(want rune) bool {
	if p.peek() != want {
		return false
	}
	p.pos += utf8.RuneLen(want)
	return true
}

func (p *sliParser) skip() bool {
	for {
		for isASCIIWhitespace(p.peek()) {
			p.pos += utf8.RuneLen(p.peek())
		}
		if p.peek() != '#' {
			return p.pos < len(p.source)
		}
		for p.pos < len(p.source) && p.peek() != '\n' {
			p.pos += utf8.RuneLen(p.peek())
		}
	}
}

func (p *sliParser) word() (string, error) {
	p.skip()
	start := p.pos
	for {
		c := p.peek()
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			break
		}
		p.pos += utf8.RuneLen(c)
	}
	if p.pos == start {
		return "", sliErr("expected identifier at byte %d", p.pos)
	}
	return p.source[start:p.pos], nil
}

func (p *sliParser) expect(want rune) error {
	p.skip()
	if !p.eat(want) {
		return sliErr("expected %q at byte %d", want, p.pos)
	}
	return nil
}

func isASCIIWhitespace(c rune) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}

func unicodeASCIIWhitespace(r rune) bool { return isASCIIWhitespace(r) }
