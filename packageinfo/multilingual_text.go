package packageinfo

import (
	"encoding/json"
	"fmt"
)

// MultilingualText represents a localized text with a default value and per-language overrides.
type MultilingualText struct {
	Default string
	Texts   map[string]string
}

// MarshalJSON implements json.Marshaler.
func (m MultilingualText) MarshalJSON() ([]byte, error) {
	if len(m.Texts) == 0 {
		return json.Marshal(m.Default)
	}

	payload := make(map[string]string, len(m.Texts)+1)
	payload["_"] = m.Default
	for key, value := range m.Texts {
		if key == "_" {
			continue
		}
		payload[key] = value
	}

	return json.Marshal(payload)
}

// UnmarshalJSON implements json.Unmarshaler.
func (m *MultilingualText) UnmarshalJSON(data []byte) error {
	if m == nil {
		return fmt.Errorf("packageinfo: nil receiver")
	}

	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		m.Default = asString
		m.Texts = nil
		return nil
	}

	var asMap map[string]string
	if err := json.Unmarshal(data, &asMap); err != nil {
		return fmt.Errorf("invalid multilingual text: %w", err)
	}

	defaultText, ok := asMap["_"]
	if !ok {
		return fmt.Errorf("multilingual text missing default key '_' ")
	}

	m.Default = defaultText
	if len(asMap) == 1 {
		m.Texts = nil
		return nil
	}

	m.Texts = make(map[string]string, len(asMap)-1)
	for key, value := range asMap {
		if key == "_" {
			continue
		}
		m.Texts[key] = value
	}
	return nil
}
