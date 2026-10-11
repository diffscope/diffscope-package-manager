package packageinfo

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
)

// SingerDeclaration is absent for every opaque contribution category. Embedding
// it preserves singer wire fields without inventing declaration state for others.
type SingerDeclaration struct {
	Manifest  map[string]any  `json:"manifest"`
	Expanded  bool            `json:"expanded"`
	Name      Text            `json:"name,omitempty"`
	Interface string          `json:"interface,omitempty"`
	Level     json.Number     `json:"level,omitempty"`
	Variant   string          `json:"variant,omitempty"`
	Singer    *SingerMetadata `json:"singer,omitempty"`
}

type SingerMetadata struct {
	Avatar     Text `json:"avatar,omitempty"`
	Background Text `json:"background,omitempty"`
	DemoAudio  Text `json:"demoAudio,omitempty"`
}

// Only singer declarations are read. Extraction does not validate singer content
// or inspect its resources, imports, exports or interpreter configuration.
func loadSingerContribution(files fs.FS, root string, scope map[string]string, c *Contribution, metadataRemaining *int64) ([]string, error) {
	c.SingerDeclaration = &SingerDeclaration{Manifest: c.Entry, Expanded: true}
	var diagnostics []string
	prefix := c.Category + "/" + c.ID + ": "
	if c.Path != "" {
		raw, err := readObject(files, c.Path)
		if err != nil {
			return nil, err
		}
		moduleScope := map[string]string{}
		for k, v := range scope {
			moduleScope[k] = v
		}
		moduleScope["dir"] = filepath.Join(root, filepath.FromSlash(path.Dir(c.Path)))
		c.Manifest, _, err = expandDocument(raw, moduleScope)
		if err != nil {
			c.Manifest = raw
			c.Expanded = false
			diagnostics = append(diagnostics, prefix+err.Error())
		}
		*metadataRemaining -= metadataCost(c.Manifest)
		if *metadataRemaining < 0 {
			return nil, fmt.Errorf("package metadata resource limit exceeded")
		}
		if !c.Expanded {
			return diagnostics, nil
		}
	}
	c.Name = Text{"_": c.ID}
	if value, exists := c.Manifest["name"]; exists {
		if t, ok := TextValue(value); ok {
			c.Name = t
		} else {
			c.Name = nil
			diagnostics = append(diagnostics, prefix+"name unavailable")
		}
	}
	if s, ok := c.Manifest["interface"].(string); ok && ValidDottedID(s) {
		c.Interface = s
	} else if _, exists := c.Manifest["interface"]; exists {
		diagnostics = append(diagnostics, prefix+"interface unavailable")
	}
	if s, ok := c.Manifest["variant"].(string); ok && ValidDottedID(s) {
		c.Variant = s
	} else if _, exists := c.Manifest["variant"]; exists {
		diagnostics = append(diagnostics, prefix+"variant unavailable")
	}
	if n, ok := positiveInteger(c.Manifest["level"]); ok {
		c.Level = n
	} else if _, exists := c.Manifest["level"]; exists {
		diagnostics = append(diagnostics, prefix+"level unavailable")
	}
	c.Singer = &SingerMetadata{}
	c.Singer.Avatar, _ = TextValue(c.Manifest["avatar"])
	c.Singer.Background, _ = TextValue(c.Manifest["background"])
	c.Singer.DemoAudio, _ = TextValue(c.Manifest["demoAudio"])
	for _, field := range []string{"avatar", "background", "demoAudio"} {
		if value, exists := c.Manifest[field]; exists {
			if _, ok := TextValue(value); !ok {
				diagnostics = append(diagnostics, prefix+field+" unavailable")
			}
		}
	}
	return diagnostics, nil
}
