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

// DecodeJSON implements the specification's UTF-8 JSON-with-comments profile.
func DecodeJSON(data []byte) (map[string]any, error) {
	if len(data) > MaxJSONBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("JSON exceeds limit or is not UTF-8")
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	clean := append([]byte(nil), data...)
	quoted, escape := false, false
	for i := 0; i < len(clean); i++ {
		c := clean[i]
		if quoted {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			continue
		}
		if c != '/' || i+1 == len(clean) {
			continue
		}
		if clean[i+1] == '/' {
			clean[i], clean[i+1] = ' ', ' '
			i += 2
			for i < len(clean) && clean[i] != '\n' && clean[i] != '\r' {
				clean[i] = ' '
				i++
			}
			i--
			continue
		}
		if clean[i+1] == '*' {
			clean[i], clean[i+1] = ' ', ' '
			i += 2
			found := false
			for i < len(clean) {
				if i+1 < len(clean) && clean[i] == '*' && clean[i+1] == '/' {
					clean[i], clean[i+1] = ' ', ' '
					i++
					found = true
					break
				}
				if clean[i] != '\n' && clean[i] != '\r' {
					clean[i] = ' '
				}
				i++
			}
			if !found {
				return nil, fmt.Errorf("unterminated JSON comment")
			}
		}
	}
	d := json.NewDecoder(bytes.NewReader(clean))
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
