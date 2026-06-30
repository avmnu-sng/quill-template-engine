// Package jsonval converts decoded JSON into Quill runtime values. It is the
// bridge the CLI and the conformance harness use to turn a .json data file into
// the variable bindings a template renders against.
//
// The mapping is deliberately lossless where Quill's value model allows it:
//
//   - a JSON object becomes an *Array preserving the member order of the source
//     file (decoding walks the json.Decoder token stream rather than unmarshaling
//     into a Go map, whose iteration order is randomized). Object keys go through
//     the engine's one canonical key model (spec 04 Section 7): a canonical
//     decimal-integer key name such as "0" becomes an Int slot, everything else
//     ("01", "name", "1.0") stays a Str key. So an object whose keys happen to be
//     "0".."n-1" is list-shaped, exactly as the key model dictates -- there is no
//     JSON-specific exception to that rule;
//   - a JSON array becomes a list-shaped *Array with 0-based integer keys;
//   - a JSON number that is an exact integer becomes an Int, otherwise a Float,
//     so {{ n }} renders 3 (not 3.0) for an integral input -- matching Quill's
//     ToText spellings (spec 04 Section 5);
//   - true/false become Bool, a string becomes Str, and null becomes Null.
//
// Decoding uses json.Number (UseNumber) so the int-vs-float decision is made on
// the literal text rather than on float64, which would collapse large integers.
package jsonval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/avmnu-sng/quill-template-engine/runtime"
)

// Decode parses JSON bytes into a single runtime.Value, preserving object key
// order. It rejects trailing content after the first value.
func Decode(data []byte) (runtime.Value, error) {
	dec := newDecoder(data)
	v, err := decodeValue(dec)
	if err != nil {
		return runtime.Null(), err
	}
	if dec.More() {
		return runtime.Null(), fmt.Errorf("decode json: trailing data after the root value")
	}
	return v, nil
}

// DecodeMap parses a JSON object into the variable map a template renders with.
// A non-object top level (an array, a scalar, or null) is an error, because the
// render entry point binds named variables.
func DecodeMap(data []byte) (map[string]runtime.Value, error) {
	dec := newDecoder(data)
	// The root must be a JSON object; peek the first token to confirm before
	// decoding so a top-level array or scalar is a clear error.
	first, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	if d, ok := first.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("data root must be a JSON object")
	}
	obj, err := decodeObject(dec)
	if err != nil {
		return nil, err
	}
	out := make(map[string]runtime.Value, obj.Arr.Len())
	for _, p := range obj.Arr.Pairs() {
		// Keys are object member names; render the (possibly canonicalized) key
		// back to its text form for the variable name.
		name, err := runtime.ToText(p.Key)
		if err != nil {
			return nil, err
		}
		out[name] = p.Val
	}
	return out, nil
}

// newDecoder builds a streaming json.Decoder that reports numbers as json.Number
// (so the int-vs-float decision is made on the literal text).
func newDecoder(data []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec
}

// decodeValue reads exactly one JSON value from the token stream and converts it
// to a runtime.Value. Objects and arrays recurse, preserving member order.
func decodeValue(dec *json.Decoder) (runtime.Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return runtime.Null(), fmt.Errorf("decode json: %w", err)
	}
	return decodeFrom(dec, tok)
}

// decodeFrom converts an already-read token, recursing into the stream for the
// container delimiters '{' and '['.
func decodeFrom(dec *json.Decoder, tok json.Token) (runtime.Value, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec)
		case '[':
			return decodeArray(dec)
		default:
			return runtime.Null(), fmt.Errorf("decode json: unexpected %q", t)
		}
	case nil:
		return runtime.Null(), nil
	case bool:
		return runtime.Bool(t), nil
	case string:
		return runtime.Str(t), nil
	case json.Number:
		return convertNumber(t)
	default:
		return runtime.Null(), fmt.Errorf("decode json: unsupported token %T", tok)
	}
}

// decodeObject reads "key": value pairs until the closing '}', building an Array
// in source order. The opening '{' has already been consumed.
func decodeObject(dec *json.Decoder) (runtime.Value, error) {
	arr := runtime.NewArray()
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return runtime.Null(), fmt.Errorf("decode json: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return runtime.Null(), fmt.Errorf("decode json: object key is not a string")
		}
		val, err := decodeValue(dec)
		if err != nil {
			return runtime.Null(), err
		}
		// Route the key through the engine's canonical key model (spec 04 Section
		// 7): SetKey -> SetStr canonicalizes a decimal-integer name ("0", "12") to
		// an Int slot and leaves every other name ("01", "name") a Str key. This is
		// intentional and not JSON-specific: an object keyed "0".."n-1" is then
		// list-shaped, just like the same keys built any other way, so the one key
		// model holds everywhere rather than JSON objects having a private rule.
		arr.SetKey(runtime.Str(key), val)
	}
	if _, err := dec.Token(); err != nil { // consume '}'
		return runtime.Null(), fmt.Errorf("decode json: %w", err)
	}
	return runtime.Arr(arr), nil
}

// decodeArray reads elements until the closing ']', assigning 0-based integer
// keys. The opening '[' has already been consumed.
func decodeArray(dec *json.Decoder) (runtime.Value, error) {
	arr := runtime.NewArray()
	var i int64
	for dec.More() {
		val, err := decodeValue(dec)
		if err != nil {
			return runtime.Null(), err
		}
		arr.SetInt(i, val)
		i++
	}
	if _, err := dec.Token(); err != nil { // consume ']'
		return runtime.Null(), fmt.Errorf("decode json: %w", err)
	}
	return runtime.Arr(arr), nil
}

// convertNumber decides Int vs Float from the literal text: an exact int64
// parse wins (so 3 is an Int), otherwise the value is a Float (so 3.5 and 1e308
// stay floats). An integer literal too large for int64 falls back to Float so it
// is not silently rejected.
func convertNumber(n json.Number) (runtime.Value, error) {
	if i, err := strconv.ParseInt(n.String(), 10, 64); err == nil {
		return runtime.Int(i), nil
	}
	f, err := n.Float64()
	if err != nil {
		return runtime.Null(), fmt.Errorf("number %q out of range", n.String())
	}
	if err := runtime.RejectNonFinite(f); err != nil {
		return runtime.Null(), err
	}
	return runtime.Float(f), nil
}
