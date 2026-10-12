package packageinfo

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// ModuleMetadata contains the required common fields of a module declaration.
type ModuleMetadata struct {
	Interface string `json:"interface"`
	Level     int64  `json:"level"`
	Variant   string `json:"variant"`
}

// SingerDeclaration records metadata extraction and is absent for opaque categories.
// ModuleMetadata is nil if required field extraction or variable expansion fails.
type SingerDeclaration struct {
	Manifest map[string]any `json:"manifest"`
	Expanded bool           `json:"expanded"`
	*ModuleMetadata
	MetadataError string          `json:"metadataError,omitempty"`
	Name          Text            `json:"name,omitempty"`
	Singer        *SingerMetadata `json:"singer,omitempty"`
}

type SingerMetadata struct {
	Avatar     Text `json:"avatar,omitempty"`
	Background Text `json:"background,omitempty"`
	DemoAudio  Text `json:"demoAudio,omitempty"`
}

// Reads singer metadata and returns diagnostics or an error for invalid JSON or exceeded limits.
// Required field extraction failures retain the declaration and set MetadataError.
// Resource contents, imports, exports, and interpreter configuration are not validated.
func loadSingerContribution(files fs.FS, root string, scope map[string]string, c *Contribution, metadataRemaining *int64) ([]string, error) {
	c.SingerDeclaration = &SingerDeclaration{Expanded: true}
	var diagnostics []string
	prefix := c.Category + "/" + c.ID + ": "
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
		c.MetadataError = fmt.Sprintf("Singer metadata expansion failed: %v", err)
		diagnostics = append(diagnostics, prefix+c.MetadataError)
	}
	*metadataRemaining -= metadataCost(c.Manifest)
	if *metadataRemaining < 0 {
		return nil, fmt.Errorf("The package metadata exceeds a resource limit.")
	}
	if !c.Expanded {
		return diagnostics, nil
	}
	c.Name = Text{"_": c.ID}
	if value, exists := c.Manifest["name"]; exists {
		if t, ok := TextValue(value); ok {
			c.Name = t
		} else {
			c.Name = nil
			diagnostics = append(diagnostics, prefix+"The name field is unavailable.")
		}
	}
	module := &ModuleMetadata{}
	var unavailable []string
	if s, ok := c.Manifest["interface"].(string); ok && ValidDottedID(s) {
		module.Interface = s
	} else {
		unavailable = append(unavailable, "interface")
	}
	if n, ok := positiveInteger(c.Manifest["level"]); ok {
		module.Level = n
	} else {
		unavailable = append(unavailable, "level")
	}
	if s, ok := c.Manifest["variant"].(string); ok && ValidDottedID(s) {
		module.Variant = s
	} else {
		unavailable = append(unavailable, "variant")
	}
	if len(unavailable) == 0 {
		c.ModuleMetadata = module
	} else {
		c.MetadataError = "Required singer metadata fields are unavailable: " + strings.Join(unavailable, ", ") + "."
		diagnostics = append(diagnostics, prefix+c.MetadataError)
	}
	c.Singer = &SingerMetadata{}
	c.Singer.Avatar, _ = TextValue(c.Manifest["avatar"])
	c.Singer.Background, _ = TextValue(c.Manifest["background"])
	c.Singer.DemoAudio, _ = TextValue(c.Manifest["demoAudio"])
	for _, field := range []string{"avatar", "background", "demoAudio"} {
		if value, exists := c.Manifest[field]; exists {
			if _, ok := TextValue(value); !ok {
				diagnostics = append(diagnostics, prefix+"The "+field+" field is unavailable.")
			}
		}
	}
	return diagnostics, nil
}
