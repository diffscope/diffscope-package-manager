package packageinfo

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const SingerDescriptionFormatVersion = "1.0"

// SingerDescription describes the singer-desc.schema.json document.
type SingerDescription struct {
	FormatVersion string                 `json:"$version" validate:"required,eq=1.0"`
	Avatar        *MultilingualText      `json:"avatar,omitempty"`
	Background    *MultilingualText      `json:"background,omitempty"`
	Class         string                 `json:"class" validate:"required"`
	Configuration *json.RawMessage       `json:"configuration,omitempty"`
	DemoAudio     SingerDemoAudios       `json:"demoAudio,omitempty" validate:"omitempty,dive"`
	ID            string                 `json:"id" validate:"required,dspm_generic_id"`
	Imports       SingerInferenceImports `json:"imports" validate:"required,dive"`
	Level         int                    `json:"level" validate:"required"`
	Name          *MultilingualText      `json:"name,omitempty"`
}

// SingerDemoAudios stores demoAudio in its normalized array form.
type SingerDemoAudios []SingerDemoAudio

// UnmarshalJSON implements json.Unmarshaler.
func (d *SingerDemoAudios) UnmarshalJSON(data []byte) error {
	if d == nil {
		return fmt.Errorf("packageinfo: nil receiver")
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("invalid singer demo audio: empty JSON")
	}

	if trimmed[0] == '[' {
		var items []SingerDemoAudio
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return fmt.Errorf("invalid singer demo audio list: %w", err)
		}
		*d = items
		return nil
	}

	var path MultilingualText
	if err := json.Unmarshal(trimmed, &path); err != nil {
		return fmt.Errorf("invalid singer demo audio path: %w", err)
	}

	*d = []SingerDemoAudio{
		{
			Name: MultilingualText{Default: ""},
			Path: path,
		},
	}
	return nil
}

// SingerDemoAudio describes a single demo audio entry.
type SingerDemoAudio struct {
	Name MultilingualText `json:"name" validate:"required"`
	Path MultilingualText `json:"path" validate:"required"`
}

// SingerInferenceImports stores imports in its normalized object form.
type SingerInferenceImports []SingerInferenceImport

// UnmarshalJSON implements json.Unmarshaler.
func (i *SingerInferenceImports) UnmarshalJSON(data []byte) error {
	if i == nil {
		return fmt.Errorf("packageinfo: nil receiver")
	}

	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return fmt.Errorf("invalid singer imports: %w", err)
	}

	items := make([]SingerInferenceImport, 0, len(rawItems))
	for index, raw := range rawItems {
		var inferenceID string
		if err := json.Unmarshal(raw, &inferenceID); err == nil {
			items = append(items, SingerInferenceImport{InferenceID: inferenceID})
			continue
		}

		var item SingerInferenceImport
		if err := json.Unmarshal(raw, &item); err != nil {
			return fmt.Errorf("invalid singer import at index %d: %w", index, err)
		}
		items = append(items, item)
	}

	*i = items
	return nil
}

// SingerInferenceImport describes an inference imported by a singer.
type SingerInferenceImport struct {
	ID          string           `json:"id,omitempty" validate:"omitempty,dspm_package_id"`
	InferenceID string           `json:"inferenceId" validate:"required,dspm_generic_id"`
	Options     *json.RawMessage `json:"options,omitempty"`
	Version     *PackageVersion  `json:"version,omitempty" validate:"omitempty,dspm_package_version"`
}
