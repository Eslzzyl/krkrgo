package tjs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	ErrRegexpMalformed   = errors.New("malformed regular expression")
	ErrRegexpUnsupported = errors.New("unsupported regular expression construct")
	// Spelling aliases retained for callers that use the shorter name.
	ErrRegexMalformed   = ErrRegexpMalformed
	ErrRegexUnsupported = ErrRegexpUnsupported
)

// RegexpError retains whether a failed compilation was a syntax error or a
// construct that Go's RE2 engine deliberately cannot represent (look-around,
// backreferences, and atomic groups).
type RegexpError struct {
	Kind    error
	Pattern string
	Err     error
}

func (e *RegexpError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("%s: %q", e.Kind, e.Pattern)
	}
	return fmt.Sprintf("%s %q: %v", e.Kind, e.Pattern, e.Err)
}
func (e *RegexpError) Unwrap() error { return e.Kind }

type Regexp struct {
	pattern string
	flags   string
	re      *regexp.Regexp
	global  bool
}
type Regex = Regexp

// CompileRegexp compiles a TJS regular expression. Supported flags are i, m,
// s, U (RE2's ungreedy mode), and g (global replacement/search metadata).
// u is accepted as a compatibility flag because Go strings are Unicode.
func CompileRegexp(pattern string, options ...string) (*Regexp, error) {
	flags := ""
	if len(options) > 1 {
		return nil, &RegexpError{Kind: ErrRegexpMalformed, Pattern: pattern, Err: fmt.Errorf("too many flag arguments")}
	}
	if len(options) == 1 {
		flags = options[0]
	}
	if err := unsupportedRegexpConstruct(pattern); err != nil {
		return nil, err
	}
	goFlags := ""
	global := false
	seen := map[byte]bool{}
	for i := 0; i < len(flags); i++ {
		f := flags[i]
		if seen[f] {
			return nil, &RegexpError{Kind: ErrRegexpMalformed, Pattern: pattern, Err: fmt.Errorf("duplicate flag %c", f)}
		}
		seen[f] = true
		switch f {
		case 'i', 'm', 's', 'U':
			goFlags += string(f)
		case 'g':
			global = true
		case 'u': /* Go regexp is UTF-8/Unicode */
		default:
			return nil, &RegexpError{Kind: ErrRegexpUnsupported, Pattern: pattern, Err: fmt.Errorf("flag %c", f)}
		}
	}
	compiledPattern := strings.ReplaceAll(pattern, `\/`, `/`)
	if goFlags != "" {
		compiledPattern = "(?" + goFlags + ")" + compiledPattern
	}
	re, err := regexp.Compile(compiledPattern)
	if err != nil {
		return nil, &RegexpError{Kind: ErrRegexpMalformed, Pattern: pattern, Err: err}
	}
	return &Regexp{pattern: pattern, flags: flags, re: re, global: global}, nil
}

func CompileRegex(pattern string, options ...string) (*Regexp, error) {
	return CompileRegexp(pattern, options...)
}
func NewRegexp(pattern string, options ...string) (*Regexp, error) {
	return CompileRegexp(pattern, options...)
}
func MustRegexp(pattern string, options ...string) *Regexp {
	r, err := CompileRegexp(pattern, options...)
	if err != nil {
		panic(err)
	}
	return r
}
func (r *Regexp) Pattern() string {
	if r == nil {
		return ""
	}
	return r.pattern
}
func (r *Regexp) Flags() string {
	if r == nil {
		return ""
	}
	return r.flags
}
func (r *Regexp) Global() bool { return r != nil && r.global }

func unsupportedRegexpConstruct(p string) error {
	// Scan escapes and character classes before identifying syntax. RE2 has no
	// backreferences or look-around; returning a typed error is preferable to
	// silently changing the meaning of a script.
	class := false
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' {
			if i+1 < len(p) {
				if p[i+1] >= '1' && p[i+1] <= '9' || p[i+1] == 'k' {
					return &RegexpError{Kind: ErrRegexpUnsupported, Pattern: p, Err: fmt.Errorf("backreference")}
				}
				i++
			}
			continue
		}
		if p[i] == '[' {
			class = true
			continue
		}
		if p[i] == ']' {
			class = false
			continue
		}
		if class {
			continue
		}
		if p[i] == '(' && i+2 < len(p) && p[i+1] == '?' {
			x := p[i+2]
			if x == '=' || x == '!' {
				return &RegexpError{Kind: ErrRegexpUnsupported, Pattern: p, Err: fmt.Errorf("look-around")}
			}
			if x == '<' && i+3 < len(p) && (p[i+3] == '=' || p[i+3] == '!') {
				return &RegexpError{Kind: ErrRegexpUnsupported, Pattern: p, Err: fmt.Errorf("look-around")}
			}
			if x == '>' {
				return &RegexpError{Kind: ErrRegexpUnsupported, Pattern: p, Err: fmt.Errorf("atomic group")}
			}
		}
	}
	return nil
}

// RegexpMatch is one match and its UTF-16 offsets. Submatch indices contain
// {-1,-1} for an unmatched optional group.
type RegexpMatch struct {
	Text            string
	Index           int
	Length          int
	Submatches      []string
	SubmatchIndices [][2]int
}
type RegexMatch = RegexpMatch

func (r *Regexp) Find(s string) (RegexpMatch, bool) {
	if r == nil || r.re == nil {
		return RegexpMatch{}, false
	}
	x := r.re.FindStringSubmatchIndex(s)
	if x == nil {
		return RegexpMatch{}, false
	}
	return makeRegexpMatch(s, x, r.re.NumSubexp()), true
}
func (r *Regexp) Match(s string) (RegexpMatch, bool) { return r.Find(s) }
func (r *Regexp) IsMatch(s string) bool              { return r != nil && r.re != nil && r.re.MatchString(s) }
func (r *Regexp) Test(s string) bool                 { return r.IsMatch(s) }
func (r *Regexp) Exec(s string) (RegexpMatch, bool)  { return r.Find(s) }
func (r *Regexp) FindAll(s string) []RegexpMatch {
	if r == nil || r.re == nil {
		return nil
	}
	ix := r.re.FindAllStringSubmatchIndex(s, -1)
	out := make([]RegexpMatch, 0, len(ix))
	for _, x := range ix {
		out = append(out, makeRegexpMatch(s, x, r.re.NumSubexp()))
	}
	return out
}
func (r *Regexp) FindAllString(s string) []string {
	if r == nil || r.re == nil {
		return nil
	}
	return r.re.FindAllString(s, -1)
}
func (r *Regexp) FindStringIndex(s string) (int, int, bool) {
	m, ok := r.Find(s)
	return m.Index, m.Index + m.Length, ok
}
func (r *Regexp) MatchString(s string) bool           { return r.IsMatch(s) }
func (r *Regexp) FindIndex(s string) (int, int, bool) { return r.FindStringIndex(s) }
func (r *Regexp) NumSubexp() int {
	if r == nil || r.re == nil {
		return 0
	}
	return r.re.NumSubexp()
}

func makeRegexpMatch(s string, ix []int, groups int) RegexpMatch {
	start, end := ix[0], ix[1]
	result := RegexpMatch{Text: s[start:end], Index: utf16ByteOffset(s, start), Length: utf16ByteOffset(s, end) - utf16ByteOffset(s, start), Submatches: make([]string, groups), SubmatchIndices: make([][2]int, groups)}
	for g := 0; g < groups; g++ {
		a, b := ix[2+g*2], ix[3+g*2]
		if a < 0 {
			result.SubmatchIndices[g] = [2]int{-1, -1}
			continue
		}
		result.Submatches[g] = s[a:b]
		result.SubmatchIndices[g] = [2]int{utf16ByteOffset(s, a), utf16ByteOffset(s, b)}
	}
	return result
}

func utf16ByteOffset(s string, byteOffset int) int {
	n := 0
	for i := 0; i < byteOffset; {
		r, size := decodeRune(s[i:])
		if size == 0 {
			break
		}
		n += len(UTF16FromString(string(r)))
		i += size
	}
	return n
}
func decodeRune(s string) (rune, int) {
	for _, r := range s {
		return r, len(string(r))
	}
	return 0, 0
}

// Replace follows JavaScript/TJS replacement tokens ($&, $1, $`, $', $$).
// Without the global flag only the first match is replaced.
func (r *Regexp) Replace(s, replacement string) string { return r.ReplaceString(s, replacement) }
func (r *Regexp) ReplaceString(s, replacement string) string {
	if r == nil || r.re == nil {
		return s
	}
	limit := 1
	if r.global {
		limit = -1
	}
	return r.replace(s, replacement, limit)
}
func (r *Regexp) ReplaceAll(s, replacement string) string {
	if r == nil || r.re == nil {
		return s
	}
	return r.replace(s, replacement, -1)
}
func (r *Regexp) ReplaceAllString(s, replacement string) string {
	return r.ReplaceAll(s, replacement)
}
func (r *Regexp) replace(s, replacement string, limit int) string {
	ix := r.re.FindAllStringSubmatchIndex(s, limit)
	if len(ix) == 0 {
		return s
	}
	var b strings.Builder
	pos := 0
	for _, m := range ix {
		b.WriteString(s[pos:m[0]])
		b.WriteString(expandReplacement(s, replacement, m, r.re.NumSubexp()))
		pos = m[1]
	}
	b.WriteString(s[pos:])
	return b.String()
}
func expandReplacement(s, repl string, m []int, groups int) string {
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		if repl[i] != '$' || i+1 >= len(repl) {
			b.WriteByte(repl[i])
			continue
		}
		i++
		switch repl[i] {
		case '$':
			b.WriteByte('$')
		case '&':
			b.WriteString(s[m[0]:m[1]])
		case '`':
			b.WriteString(s[:m[0]])
		case '\'':
			b.WriteString(s[m[1]:])
		default:
			if repl[i] < '0' || repl[i] > '9' {
				b.WriteByte('$')
				b.WriteByte(repl[i])
				continue
			}
			n := int(repl[i] - '0')
			if i+1 < len(repl) && repl[i+1] >= '0' && repl[i+1] <= '9' {
				n = n*10 + int(repl[i+1]-'0')
				i++
			}
			if n == 0 || n > groups || m[2*n] < 0 {
				continue
			}
			b.WriteString(s[m[2*n]:m[2*n+1]])
		}
	}
	return b.String()
}

// Split returns text between matches. Captures are not returned, matching
// tjsRegExp::Split and Rust regex::Regex::split. limit <= 0 means unlimited.
func (r *Regexp) Split(s string, limit int) []string {
	if r == nil || r.re == nil {
		return []string{s}
	}
	ix := r.re.FindAllStringIndex(s, -1)
	out := make([]string, 0, len(ix)+1)
	pos := 0
	for _, m := range ix {
		// TJS split ignores zero-width matches (ONIG_OPTION_FIND_NOT_EMPTY).
		if m[0] == m[1] {
			continue
		}
		if limit > 0 && len(out) >= limit-1 {
			break
		}
		out = append(out, s[pos:m[0]])
		pos = m[1]
	}
	if limit <= 0 || len(out) < limit {
		out = append(out, s[pos:])
	}
	return out
}

func (r *Regexp) SplitString(s string) []string { return r.Split(s, 0) }
func (r *Regexp) SplitPurgeEmpty(s string, purgeEmpty bool) []string {
	parts := r.Split(s, 0)
	if !purgeEmpty || len(parts) == 1 {
		return parts
	}
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
func (r *Regexp) SplitWithLimit(s string, limit int, purgeEmpty bool) []string {
	parts := r.Split(s, limit)
	if !purgeEmpty || len(parts) == 1 {
		return parts
	}
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// RegexpEscape quotes regexp metacharacters, equivalent to regex.escape in the
// Rust implementation.
func RegexpEscape(s string) string { return regexp.QuoteMeta(s) }
func RegexEscape(s string) string  { return RegexpEscape(s) }
