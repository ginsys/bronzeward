package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"unicode/utf8"
)

// Kind is a secret's stored kind (compilation.md §5.2, choice §16.16): a declaration's kind must
// equal it.
type Kind string

const (
	KindString  Kind = "string"
	KindInteger Kind = "integer"
	KindBoolean Kind = "boolean"
	KindMapping Kind = "mapping" // an object whose members are of the three scalar kinds
)

// Value is a generation's content: its kind and the JSON value of that kind. A generation's KV v2
// data is {"kind": <Kind>, "value": <JSON>} (compilation.md choice §16.28); a reader decodes an
// integer as a JSON number with UseNumber. NewValue builds a checked one, and CreateGeneration
// checks again whatever it is given. A Value renders as a placeholder under every fmt verb.
type Value struct {
	Kind Kind
	JSON json.RawMessage
}

const valueText = "[provider value]"

func (Value) Format(f fmt.State, _ rune) { io.WriteString(f, valueText) }

// NewValue checks v against k and encodes it: a Go string, an integer type or an integer
// json.Number, a bool, or a map[string]string / map[string]any of those. Floats, nil, lists,
// nested mappings and invalid UTF-8 (which JSON encoding would silently replace) are refused.
// Errors never quote v.
func NewValue(k Kind, v any) (Value, error) {
	var got Kind
	switch x := v.(type) {
	case map[string]string:
		for key, member := range x {
			if !utf8.ValidString(key) || !utf8.ValidString(member) {
				return Value{}, errors.New("provider: a mapping key or member is not valid UTF-8")
			}
		}
		got = KindMapping
	case map[string]any:
		for key, member := range x {
			if !utf8.ValidString(key) {
				return Value{}, errors.New("provider: a mapping key is not valid UTF-8")
			}
			if _, err := scalarGoKind(member); err != nil {
				return Value{}, fmt.Errorf("provider: a mapping member: %w", err)
			}
		}
		got = KindMapping
	default:
		var err error
		if got, err = scalarGoKind(v); err != nil {
			return Value{}, err
		}
	}
	if got != k {
		return Value{}, fmt.Errorf("provider: a %s given for kind %q", got, k)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return Value{}, errors.New("provider: the value could not be encoded")
	}
	val := Value{Kind: k, JSON: b}
	if err := val.check(); err != nil {
		return Value{}, err
	}
	return val, nil
}

func scalarGoKind(v any) (Kind, error) {
	switch x := v.(type) {
	case string:
		if !utf8.ValidString(x) {
			return "", errors.New("provider: a string is not valid UTF-8")
		}
		return KindString, nil
	case bool:
		return KindBoolean, nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return KindInteger, nil
	case json.Number:
		if !integerNumber(string(x)) {
			return "", errors.New("provider: a number that is not an integer")
		}
		return KindInteger, nil
	case float32, float64:
		return "", errors.New("provider: a float; the kinds are string, integer, boolean and mapping")
	case nil:
		return "", errors.New("provider: a null; the kinds are string, integer, boolean and mapping")
	default:
		return "", fmt.Errorf("provider: a %T; the kinds are string, integer, boolean and mapping", v)
	}
}

var integerSyntax = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// integerNumber is an integer literal within int64 or uint64.
func integerNumber(s string) bool {
	if !integerSyntax.MatchString(s) {
		return false
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

// check holds v's JSON to its kind: exactly one value, valid UTF-8, no float, null, list, nested
// mapping or repeated mapping member. Errors never quote the value.
func (v Value) check() error {
	if !utf8.Valid(v.JSON) {
		return errors.New("provider: the value is not valid UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(v.JSON))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return errors.New("provider: the value is not JSON")
	}
	switch v.Kind {
	case KindString, KindInteger, KindBoolean:
		if scalarJSONKind(tok) != v.Kind {
			return fmt.Errorf("provider: the value is not of kind %q", v.Kind)
		}
	case KindMapping:
		if tok != json.Delim('{') {
			return errors.New(`provider: the value is not of kind "mapping"`)
		}
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return errors.New("provider: the value is not JSON")
			}
			name, _ := key.(string)
			if seen[name] {
				return errors.New("provider: a mapping member is repeated")
			}
			seen[name] = true
			member, err := dec.Token()
			if err != nil {
				return errors.New("provider: the value is not JSON")
			}
			if scalarJSONKind(member) == "" {
				return errors.New("provider: a mapping member is not a string, integer or boolean")
			}
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') {
			return errors.New("provider: the value is not JSON")
		}
	default:
		return errors.New("provider: an unknown kind")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("provider: the value holds more than one JSON value")
	}
	return nil
}

func scalarJSONKind(tok json.Token) Kind {
	switch x := tok.(type) {
	case string:
		return KindString
	case bool:
		return KindBoolean
	case json.Number:
		if integerNumber(string(x)) {
			return KindInteger
		}
	}
	return ""
}
