package tjs

import "fmt"

// Literal is a constant tree emitted by the compiler. Dictionary entries stay
// as a slice (rather than a Go map) so duplicate keys and source order survive.
type LiteralKind uint8

const (
	LiteralValueKind LiteralKind = iota
	LiteralArrayKind
	LiteralDictionaryKind
)

type DictionaryEntry struct {
	Key   Literal
	Value Literal
}
type Literal struct {
	kind       LiteralKind
	value      Value
	array      []Literal
	dictionary []DictionaryEntry
}

func ValueLiteral(value Value) Literal { return Literal{kind: LiteralValueKind, value: value} }
func ArrayLiteral(items ...Literal) Literal {
	return Literal{kind: LiteralArrayKind, array: append([]Literal(nil), items...)}
}
func DictionaryLiteral(entries ...DictionaryEntry) Literal {
	return Literal{kind: LiteralDictionaryKind, dictionary: append([]DictionaryEntry(nil), entries...)}
}
func DictEntry(key, value Literal) DictionaryEntry { return DictionaryEntry{Key: key, Value: value} }
func (l Literal) Kind() LiteralKind                { return l.kind }
func (l Literal) Value() (Value, bool)             { return l.value, l.kind == LiteralValueKind }
func (l Literal) Array() []Literal                 { return append([]Literal(nil), l.array...) }
func (l Literal) Dictionary() []DictionaryEntry {
	return append([]DictionaryEntry(nil), l.dictionary...)
}

func (l Literal) Validate() error { return l.ValidateDepth(0) }
func (l Literal) ValidateDepth(depth int) error {
	if depth >= 128 {
		return fmt.Errorf("constant literal nesting exceeds limit")
	}
	switch l.kind {
	case LiteralValueKind:
		if l.value.kind == KindObject && !l.value.object.IsNull() {
			return fmt.Errorf("object handle in constant literal")
		}
	case LiteralArrayKind:
		if len(l.array) > 100000 {
			return fmt.Errorf("constant array exceeds limit")
		}
		for i := range l.array {
			if err := l.array[i].ValidateDepth(depth + 1); err != nil {
				return err
			}
		}
	case LiteralDictionaryKind:
		if len(l.dictionary) > 100000 {
			return fmt.Errorf("constant dictionary exceeds limit")
		}
		for i := range l.dictionary {
			if err := l.dictionary[i].Key.ValidateDepth(depth + 1); err != nil {
				return err
			}
			if err := l.dictionary[i].Value.ValidateDepth(depth + 1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid literal kind %d", l.kind)
	}
	return nil
}

// Clone performs a complete value/tree copy and is useful when a compiler
// shares a constant pool between script blocks.
func (l Literal) Clone() Literal {
	r := Literal{kind: l.kind, value: l.value}
	if l.kind == LiteralArrayKind {
		r.array = make([]Literal, len(l.array))
		for i := range l.array {
			r.array[i] = l.array[i].Clone()
		}
	}
	if l.kind == LiteralDictionaryKind {
		r.dictionary = make([]DictionaryEntry, len(l.dictionary))
		for i := range l.dictionary {
			r.dictionary[i] = DictionaryEntry{Key: l.dictionary[i].Key.Clone(), Value: l.dictionary[i].Value.Clone()}
		}
	}
	return r
}
