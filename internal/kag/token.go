package kag

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/eslzzyl/krkrgo/internal/core"
)

// Attribute is one ordered KAG tag attribute. Attribute order is significant,
// and duplicate names are retained. Entity marks a leading & TJS expression.
type Attribute struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Entity bool   `json:"entity,omitempty"`
}

// Tag is a KAG command and its ordered attributes.
type Tag struct {
	Name       string      `json:"name"`
	Attributes []Attribute `json:"attributes"`
}

// Get returns the last attribute with name. KAGParserEx resolves duplicate
// attributes from right to left while retaining their original order.
func (t Tag) Get(name string) (string, bool) {
	for i := len(t.Attributes) - 1; i >= 0; i-- {
		if t.Attributes[i].Name == name {
			return t.Attributes[i].Value, true
		}
	}
	return "", false
}

// Lookup is an alias for Get for callers that prefer map-like terminology.
func (t Tag) Lookup(name string) (string, bool) { return t.Get(name) }

// MarshalJSON keeps Tag's wire shape identical to Rust's Vec<(String,String)>:
// attributes are arrays of pairs rather than an unordered object.
func (t Tag) MarshalJSON() ([]byte, error) {
	attributes := make([][2]string, len(t.Attributes))
	for i := range t.Attributes {
		attributes[i] = [2]string{t.Attributes[i].Name, t.Attributes[i].Value}
	}
	return json.Marshal(struct {
		Name       string      `json:"name"`
		Attributes [][2]string `json:"attributes"`
	}{t.Name, attributes})
}

// MarshalJSON serializes the externally tagged Rust Kind enum while retaining
// a compact discriminator for Go callers.
func (t Token) MarshalJSON() ([]byte, error) {
	var kind any
	switch t.Kind {
	case KindText:
		kind = struct {
			Text string `json:"Text"`
		}{t.Text}
	case KindTag:
		if t.Tag == nil {
			return nil, fmt.Errorf("KAG tag token has no tag payload")
		}
		kind = struct {
			Tag Tag `json:"Tag"`
		}{*t.Tag}
	case KindLabel:
		if t.Label == nil {
			return nil, fmt.Errorf("KAG label token has no label payload")
		}
		kind = struct {
			Label struct {
				Name    string  `json:"name"`
				Caption *string `json:"caption"`
			} `json:"Label"`
		}{Label: struct {
			Name    string  `json:"name"`
			Caption *string `json:"caption"`
		}{t.Label.Name, t.Label.Caption}}
	case KindNewline:
		kind = "Newline"
	default:
		return nil, fmt.Errorf("invalid KAG token kind %d", t.Kind)
	}
	kindJSON, err := json.Marshal(kind)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Location     core.SourceLocation `json:"location"`
		Kind         json.RawMessage     `json:"kind"`
		InlineScript *InlineScript       `json:"inline_script,omitempty"`
	}{t.Location, kindJSON, t.InlineScript})
}

// UnmarshalJSON accepts Rust's ordered attribute-pair representation.
func (t *Tag) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name       string      `json:"name"`
		Attributes [][2]string `json:"attributes"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	attributes := make([]Attribute, len(wire.Attributes))
	for i, pair := range wire.Attributes {
		attributes[i] = Attribute{Name: pair[0], Value: pair[1]}
	}
	t.Name = wire.Name
	t.Attributes = attributes
	return nil
}

// UnmarshalJSON accepts Rust's externally tagged Kind representation.
func (t *Token) UnmarshalJSON(data []byte) error {
	var wire struct {
		Location     core.SourceLocation `json:"location"`
		Kind         json.RawMessage     `json:"kind"`
		InlineScript *InlineScript       `json:"inline_script,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	t.Location = wire.Location
	t.Text = ""
	t.Tag = nil
	t.Label = nil
	t.InlineScript = wire.InlineScript
	kind := bytes.TrimSpace(wire.Kind)
	if len(kind) == 0 {
		return fmt.Errorf("missing KAG token kind")
	}
	if kind[0] == '"' {
		var name string
		if err := json.Unmarshal(kind, &name); err != nil {
			return err
		}
		if name != "Newline" {
			return fmt.Errorf("unknown KAG token kind %q", name)
		}
		t.Kind = KindNewline
		return nil
	}
	var variants struct {
		Text  *string `json:"Text"`
		Tag   *Tag    `json:"Tag"`
		Label *struct {
			Name    string  `json:"name"`
			Caption *string `json:"caption"`
		} `json:"Label"`
	}
	if err := json.Unmarshal(kind, &variants); err != nil {
		return err
	}
	switch {
	case variants.Text != nil:
		t.Kind, t.Text = KindText, *variants.Text
	case variants.Tag != nil:
		t.Kind, t.Tag = KindTag, variants.Tag
	case variants.Label != nil:
		t.Kind = KindLabel
		t.Label = &Label{Name: variants.Label.Name, Caption: variants.Label.Caption}
	default:
		return fmt.Errorf("unknown KAG token kind")
	}
	return nil
}

// Kind identifies the payload carried by a Token.
type Kind uint8

const (
	KindText Kind = iota
	KindTag
	KindLabel
	KindNewline
)

// Descriptive aliases for the Kind constants.
const (
	TextKind    = KindText
	TagKind     = KindTag
	LabelKind   = KindLabel
	NewlineKind = KindNewline
)

// InlineScript is source collected from a physical KAG iscript block. StartLine
// is the 1-based source line of its first body line; LineCount excludes the
// opening and closing markers.
type InlineScript struct {
	Source    string `json:"source"`
	StartLine uint64 `json:"start_line"`
	LineCount uint64 `json:"line_count"`
}

// Label is the payload of a label token.
type Label struct {
	Name    string  `json:"name"`
	Caption *string `json:"caption,omitempty"`
}

// Token is one deterministic scenario token. Only the payload corresponding
// to Kind is populated.
type Token struct {
	Location     core.SourceLocation `json:"location"`
	Kind         Kind                `json:"kind"`
	Text         string              `json:"text,omitempty"`
	Tag          *Tag                `json:"tag,omitempty"`
	Label        *Label              `json:"label,omitempty"`
	InlineScript *InlineScript       `json:"inline_script,omitempty"`
}

// Scenario is a parsed KAG script, its label index, and its physical source lines.
type Scenario struct {
	Storage     string         `json:"storage"`
	Tokens      []Token        `json:"tokens"`
	Labels      map[string]int `json:"labels"`
	SourceLines []string       `json:"source_lines,omitempty"`
}

// Cursor is a restorable parser position, current physical line, and return stack.
// CurrentLine is 1-based; zero means it is unavailable in a legacy scenario.
type Cursor struct {
	Position    int   `json:"position"`
	Calls       []int `json:"calls"`
	CurrentLine int   `json:"current_line,omitempty"`
}

// Parser incrementally consumes a Scenario.
type Parser struct {
	scenario Scenario
	cursor   Cursor
}

// NewParser starts a parser at the beginning of scenario.
func NewParser(scenario Scenario) *Parser {
	cursor := Cursor{Calls: []int{}}
	if len(scenario.SourceLines) != 0 {
		cursor.CurrentLine = 1
	}
	return &Parser{scenario: scenario, cursor: cursor}
}

// Scenario returns the parser's immutable scenario value.
func (p *Parser) Scenario() Scenario { return p.scenario }

// CurrentLineStr returns the original physical source line last read by the
// parser. Labels and inline-script blocks do not replace it.
func (p *Parser) CurrentLineStr() string {
	if p == nil || p.cursor.CurrentLine < 1 || p.cursor.CurrentLine > len(p.scenario.SourceLines) {
		return ""
	}
	return p.scenario.SourceLines[p.cursor.CurrentLine-1]
}

func (p *Parser) physicalLineAtPosition(position int) int {
	if position >= 0 && position < len(p.scenario.Tokens) {
		line := int(p.scenario.Tokens[position].Location.Line)
		if line > 0 && line <= len(p.scenario.SourceLines) {
			return line
		}
	}
	if position == len(p.scenario.Tokens) && len(p.scenario.Tokens) != 0 {
		line := int(p.scenario.Tokens[len(p.scenario.Tokens)-1].Location.Line)
		if line > 0 && line <= len(p.scenario.SourceLines) {
			return line
		}
	}
	return 0
}

func (p *Parser) lineAtPreviousToken(position int) int {
	if position <= 0 {
		if len(p.scenario.SourceLines) != 0 {
			return 1
		}
		return 0
	}
	for i := position - 1; i >= 0; i-- {
		token := &p.scenario.Tokens[i]
		if token.Kind == KindLabel || token.InlineScript != nil {
			continue
		}
		line := int(token.Location.Line)
		if line > 0 && line <= len(p.scenario.SourceLines) {
			return line
		}
	}
	if len(p.scenario.SourceLines) != 0 {
		return 1
	}
	return 0
}

func (p *Parser) updateCurrentLine(token *Token) {
	if token.Kind == KindLabel || token.InlineScript != nil {
		return
	}
	if line := int(token.Location.Line); line > 0 && line <= len(p.scenario.SourceLines) {
		p.cursor.CurrentLine = line
	}
}

// State returns a copy of the parser cursor suitable for Restore.
func (p *Parser) State() Cursor {
	state := p.cursor
	state.Calls = append([]int{}, p.cursor.Calls...)
	return state
}

// NextToken returns and consumes the next token, or nil at end of scenario.
func (p *Parser) NextToken() *Token {
	if p.cursor.Position < 0 || p.cursor.Position >= len(p.scenario.Tokens) {
		return nil
	}
	token := &p.scenario.Tokens[p.cursor.Position]
	p.cursor.Position++
	p.updateCurrentLine(token)
	return token
}

// Next is a short alias for NextToken.
func (p *Parser) Next() *Token { return p.NextToken() }
