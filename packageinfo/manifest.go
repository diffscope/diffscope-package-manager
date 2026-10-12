package packageinfo

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type Text map[string]string

const MaxPackageMetadataBytes = 64 << 20

func metadataCost(v any) int64 {
	cost := int64(32)
	switch x := v.(type) {
	case string:
		cost += int64(len(x))
	case json.Number:
		cost += int64(len(x))
	case map[string]any:
		for k, v := range x {
			cost += int64(len(k)) + metadataCost(v)
		}
	case []any:
		for _, v := range x {
			cost += metadataCost(v)
		}
	}
	return cost
}

func TextValue(v any) (Text, bool) {
	if s, ok := v.(string); ok {
		return Text{"_": s}, true
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	t := Text{}
	for k, v := range m {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		t[k] = s
	}
	_, ok = t["_"]
	return t, ok
}

type Dependency struct {
	ID      string  `json:"id"`
	Version Version `json:"version"`
}
type Contribution struct {
	Category string         `json:"category"`
	ID       string         `json:"id"`
	Path     string         `json:"path,omitempty"`
	Entry    map[string]any `json:"entry"`
	*SingerDeclaration
}
type PackageManifest struct {
	Identity
	FormatVersion Version        `json:"formatVersion"`
	RuntimeLevel  int            `json:"runtimeLevel"`
	CompatVersion Version        `json:"compatVersion"`
	Dependencies  []Dependency   `json:"dependencies"`
	Contributions []Contribution `json:"contributions"`
	Name          Text           `json:"name,omitempty"`
	Description   Text           `json:"description,omitempty"`
	Vendor        Text           `json:"vendor,omitempty"`
	License       Text           `json:"license,omitempty"`
	Readme        Text           `json:"readme,omitempty"`
	URL           *string        `json:"url,omitempty"`
	Manifest      map[string]any `json:"manifest"`
	Diagnostics   []string       `json:"diagnostics,omitempty"`
}

func readObject(files fs.FS, name string) (map[string]any, error) {
	f, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("The path %s is not a regular file.", name)
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxJSONBytes+1))
	if err != nil {
		return nil, err
	}
	m, err := DecodeJSON(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return m, nil
}
func versionField(m map[string]any, k string) (Version, error) {
	s, ok := m[k].(string)
	if !ok {
		return Version{}, fmt.Errorf("The field %s must be a version string.", k)
	}
	return ParseVersion(s)
}
func positiveInteger(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil && i > 0
}

// Reads package metadata and returns the manifest or an error for invalid declarations or references.
// The root argument specifies a fully qualified lexical root for variable expansion.
func Load(files fs.FS, root string) (PackageManifest, error) {
	var p PackageManifest
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) || !utf8.ValidString(root) || strings.ContainsRune(root, 0) || strings.Contains(root, "\\") && filepath.Separator != '\\' {
		return p, fmt.Errorf("The package root must be a representable, fully qualified path.")
	}
	raw, err := readObject(files, "desc.json")
	if err != nil {
		return p, err
	}
	p.FormatVersion, err = versionField(raw, "$version")
	if err != nil {
		return p, err
	}
	one, _ := ParseVersion("1")
	if p.FormatVersion.Compare(one) != 0 {
		return p, fmt.Errorf("The manifest format is unsupported.")
	}
	level, ok := positiveInteger(raw["runtimeLevel"])
	if !ok || level != 1 {
		return p, fmt.Errorf("The runtimeLevel value is unsupported or invalid.")
	}
	p.RuntimeLevel = 1
	m, scope, err := expandDocument(raw, map[string]string{"root": root, "dir": root})
	if err != nil {
		return p, err
	}
	p.Manifest = m
	metadataRemaining := int64(MaxPackageMetadataBytes) - metadataCost(m)
	if metadataRemaining < 0 {
		return p, fmt.Errorf("The package metadata exceeds a resource limit.")
	}
	p.ID, ok = m["id"].(string)
	if !ok || !IsValidPackageIdentifier(p.ID) {
		return p, fmt.Errorf("The package ID is invalid.")
	}
	p.Version, err = versionField(m, "version")
	if err != nil {
		return p, err
	}
	p.CompatVersion = p.Version
	if _, ok := m["compatVersion"]; ok {
		p.CompatVersion, err = versionField(m, "compatVersion")
		if err != nil {
			return p, err
		}
	}
	if p.CompatVersion.Compare(p.Version) > 0 {
		return p, fmt.Errorf("The compatVersion value exceeds the package version.")
	}
	for _, field := range []string{"name", "vendor", "description", "license", "readme"} {
		if v, exists := m[field]; exists {
			t, ok := TextValue(v)
			if !ok {
				return p, fmt.Errorf("The multilingual field %s is invalid.", field)
			}
			switch field {
			case "name":
				p.Name = t
			case "vendor":
				p.Vendor = t
			case "description":
				p.Description = t
			case "license", "readme":
				for language, value := range t {
					name, err := directFile(files, root, value)
					if err != nil {
						return p, fmt.Errorf("%s: %w", field, err)
					}
					t[language] = filepath.Join(root, filepath.FromSlash(name))
				}
				if field == "license" {
					p.License = t
				} else {
					p.Readme = t
				}
			}
		}
	}
	if v, ok := m["url"]; ok {
		url, ok := v.(string)
		if !ok {
			return p, fmt.Errorf("The url field must be a string.")
		}
		p.URL = &url
	}
	p.Dependencies = []Dependency{}
	p.Contributions = []Contribution{}
	if v, exists := m["dependencies"]; exists {
		a, ok := v.([]any)
		if !ok {
			return p, fmt.Errorf("The dependencies field must be an array.")
		}
		seen := map[string]bool{}
		for _, v := range a {
			d, ok := v.(map[string]any)
			if !ok {
				return p, fmt.Errorf("Each dependency must be an object.")
			}
			id, ok := d["id"].(string)
			if !ok || !IsValidPackageIdentifier(id) || seen[id] || id == p.ID {
				return p, fmt.Errorf("The dependency %q is invalid, duplicate, or self-referential.", id)
			}
			seen[id] = true
			version, err := versionField(d, "version")
			if err != nil {
				return p, err
			}
			p.Dependencies = append(p.Dependencies, Dependency{id, version})
		}
	}
	if value, exists := m["contributions"]; exists {
		categories, ok := value.(map[string]any)
		if !ok {
			return p, fmt.Errorf("The contributions field must be an object.")
		}
		keys := make([]string, 0, len(categories))
		for c := range categories {
			keys = append(keys, c)
		}
		sort.Strings(keys)
		for _, category := range keys {
			if !ValidDottedID(category) {
				return p, fmt.Errorf("The category %q is invalid.", category)
			}
			a, ok := categories[category].([]any)
			if !ok {
				return p, fmt.Errorf("The contribution category %s must contain an array.", category)
			}
			seen := map[string]bool{}
			for _, v := range a {
				entry, ok := v.(map[string]any)
				if !ok {
					return p, fmt.Errorf("Each contribution must be an object.")
				}
				id, ok := entry["id"].(string)
				if !ok || !IsValidGenericIdentifier(id) || seen[id] {
					return p, fmt.Errorf("The contribution ID %q is invalid or duplicate.", id)
				}
				seen[id] = true
				c := Contribution{Category: category, ID: id, Entry: entry}
				if v, exists := entry["path"]; exists {
					value, ok := v.(string)
					if !ok && category == "singer" {
						return p, fmt.Errorf("The contribution path must be a string.")
					}
					if ok {
						c.Path, err = directFile(files, root, value)
						if err != nil {
							return p, err
						}
					}
				}
				if category == "singer" {
					if c.Path == "" {
						return p, fmt.Errorf("Singer contribution %q requires a declaration path.", id)
					}
					diagnostics, err := loadSingerContribution(files, root, scope, &c, &metadataRemaining)
					if err != nil {
						return p, err
					}
					p.Diagnostics = append(p.Diagnostics, diagnostics...)
				}
				p.Contributions = append(p.Contributions, c)
			}
		}
	}
	return p, nil
}

func directFile(files fs.FS, root, value string) (string, error) {
	if strings.ContainsRune(value, 0) || value == "" {
		return "", fmt.Errorf("The direct file path is invalid.")
	}
	value = strings.ReplaceAll(value, "\\", "/")
	if filepath.Separator == '\\' && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		value = filepath.ToSlash(filepath.VolumeName(root)) + value
	}
	if filepath.VolumeName(filepath.FromSlash(value)) != "" && !filepath.IsAbs(filepath.FromSlash(value)) {
		return "", fmt.Errorf("The drive-relative file reference %q is ambiguous.", value)
	}
	resolved := filepath.Clean(filepath.Join(root, filepath.FromSlash(value)))
	if filepath.IsAbs(filepath.FromSlash(value)) {
		resolved = filepath.Clean(filepath.FromSlash(value))
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	name := filepath.ToSlash(relative)
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, ":") {
		return "", fmt.Errorf("The direct file reference %q is outside the package.", value)
	}
	st, err := fs.Stat(files, name)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("The direct reference %q is not a regular file.", value)
	}
	return name, nil
}
