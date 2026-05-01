package packageinfo

import "encoding/json"

const InferenceDescriptionFormatVersion = "1.0"

// InferenceDescription describes the inference-desc.schema.json document.
type InferenceDescription struct {
	FormatVersion string            `json:"$version" validate:"required,eq=1.0"`
	Class         string            `json:"class" validate:"required"`
	Configuration *json.RawMessage  `json:"configuration,omitempty"`
	ID            string            `json:"id" validate:"required,dspm_generic_id"`
	Level         int               `json:"level" validate:"required"`
	Name          *MultilingualText `json:"name,omitempty"`
	Schema        *json.RawMessage  `json:"schema,omitempty"`
}
