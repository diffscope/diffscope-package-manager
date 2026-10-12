package packageinfo

import (
	"fmt"
	"strings"
)

func validVar(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range []byte(s) {
		if c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// Returns the expanded template or an error for invalid references or exceeded limits.
// Replacement text is not rescanned.
func expand(s string, scope map[string]string) (string, error) {
	pos := 0
	var parse func(bool, int) (string, error)
	parse = func(nested bool, depth int) (string, error) {
		if depth > maxJSONDepth {
			return "", fmt.Errorf("Variable nesting exceeds its limit.")
		}
		var b strings.Builder
		for pos < len(s) {
			if nested && s[pos] == '}' {
				pos++
				return b.String(), nil
			}
			if s[pos] == '$' && pos+1 < len(s) {
				switch s[pos+1] {
				case '$':
					b.WriteByte('$')
					pos += 2
					continue
				case '{':
					pos += 2
					name, err := parse(true, depth+1)
					if err != nil {
						return "", err
					}
					if validVar(name) {
						b.WriteString(scope[name])
					}
					if b.Len() > MaxJSONBytes {
						return "", fmt.Errorf("The expanded string exceeds its byte limit.")
					}
					continue
				}
			}
			b.WriteByte(s[pos])
			pos++
		}
		if nested {
			return "", fmt.Errorf("The template contains an unpaired variable reference.")
		}
		return b.String(), nil
	}
	return parse(false, 0)
}
func expandDocument(raw map[string]any, outer map[string]string) (map[string]any, map[string]string, error) {
	scope := map[string]string{}
	for k, v := range outer {
		scope[k] = v
	}
	if variables, ok := raw["vars"]; ok {
		variableBudget := MaxJSONBytes
		a, ok := variables.([]any)
		if !ok {
			return nil, nil, fmt.Errorf("The vars field must be an array.")
		}
		seen := map[string]bool{}
		for _, value := range a {
			m, ok := value.(map[string]any)
			if !ok || len(m) != 2 {
				return nil, nil, fmt.Errorf("A variable must contain only name and value fields.")
			}
			name, nok := m["name"].(string)
			v, vok := m["value"].(string)
			if !nok || !vok || !validVar(name) || name == "root" || name == "dir" || seen[name] {
				return nil, nil, fmt.Errorf("The variable name %q is invalid or repeated.", name)
			}
			seen[name] = true
			e, err := expand(v, scope)
			if err != nil {
				return nil, nil, err
			}
			scope[name] = e
			variableBudget -= len(e)
			if variableBudget < 0 {
				return nil, nil, fmt.Errorf("The expanded variable scope exceeds its byte limit.")
			}
		}
	}
	budget := MaxJSONBytes
	var walk func(any, string) (any, error)
	walk = func(v any, key string) (any, error) {
		switch x := v.(type) {
		case string:
			if key == "$version" {
				return x, nil
			}
			e, err := expand(x, scope)
			budget -= len(e)
			if budget < 0 {
				return nil, fmt.Errorf("The expanded JSON exceeds its byte limit.")
			}
			return e, err
		case map[string]any:
			m := map[string]any{}
			for k, v := range x {
				e, err := walk(v, k)
				if err != nil {
					return nil, err
				}
				m[k] = e
			}
			return m, nil
		case []any:
			a := make([]any, len(x))
			for i, v := range x {
				e, err := walk(v, "")
				if err != nil {
					return nil, err
				}
				a[i] = e
			}
			return a, nil
		}
		return v, nil
	}
	document := make(map[string]any, len(raw))
	for k, v := range raw {
		if k != "vars" {
			document[k] = v
		}
	}
	v, err := walk(document, "")
	if err != nil {
		return nil, nil, err
	}
	return v.(map[string]any), scope, nil
}
