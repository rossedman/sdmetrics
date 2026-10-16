package scrape

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// Kind is the kind of a metric value.
type Kind uint8

// Value kinds.
const (
	// KindInt is an integer that fits int64. systemd's own sources send
	// unsigned integers.
	KindInt Kind = iota + 1
	// KindFloat is any other number, including integers beyond int64.
	KindFloat
	// KindString is a string, the value of TypeString families.
	KindString
)

// Value is one decoded metric value.
type Value struct {
	Kind  Kind
	Int   int64
	Float float64
	Str   string
}

// Number returns a numeric value as float64.
func (v Value) Number() float64 {
	if v.Kind == KindInt {
		return float64(v.Int)
	}
	return v.Float
}

func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return strconv.FormatInt(v.Int, 10)
	case KindFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case KindString:
		return strconv.Quote(v.Str)
	}
	return "<invalid>"
}

// decodeValue decodes a List reply's value.
func decodeValue(raw json.RawMessage) (Value, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return Value{}, fmt.Errorf("missing value")
	}
	switch c := raw[0]; {
	case c == '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return Value{}, err
		}
		return Value{Kind: KindString, Str: s}, nil
	case c == '-' || (c >= '0' && c <= '9'):
		text := string(raw)
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return Value{Kind: KindInt, Int: i}, nil
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsInf(f, 0) {
			return Value{}, fmt.Errorf("bad number %s", text)
		}
		return Value{Kind: KindFloat, Float: f}, nil
	}
	return Value{}, fmt.Errorf("unsupported value %.32s", raw)
}

// decodeFields decodes a List reply's fields. systemd sends string
// values; anything else is kept as its JSON text.
func decodeFields(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("fields: %w", err)
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			s = string(v)
		}
		out[k] = s
	}
	return out, nil
}
