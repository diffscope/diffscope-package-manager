package packageinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

const MaxJSONBytes = 16 << 20
const maxJSONDepth = 128
const maxJSONNodes = 1_000_000

// Decodes a UTF-8 JSON object without a byte order mark or comments.
// Returns an error for invalid syntax, duplicate keys, or exceeded resource limits.
func DecodeJSON(data []byte) (map[string]any, error) {
	if len(data) > MaxJSONBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("JSON exceeds limit or is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	nodes := 0
	value, err := decodeValue(d, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data: %v", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("declaration must be a JSON object")
	}
	return object, nil
}
func decodeValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > maxJSONDepth || *nodes > maxJSONNodes {
		return nil, fmt.Errorf("JSON resource limit exceeded")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("object key must be string")
			}
			if _, ok := m[key]; ok {
				return nil, fmt.Errorf("duplicate JSON key %q", key)
			}
			v, err := decodeValue(d, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		_, err = d.Token()
		return m, err
	case '[':
		a := []any{}
		for d.More() {
			v, err := decodeValue(d, depth+1, nodes)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err = d.Token()
		return a, err
	}
	return nil, fmt.Errorf("unexpected JSON delimiter")
}
