// Package packagerepository implements the OVCPR v1 wire format and publication
// projection. It does not access configuration, databases, Git or the network.
package packagerepository

import (
	"bytes"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

type SingerSummary struct {
	ID        string           `json:"id"`
	Name      packageinfo.Text `json:"name"`
	Interface string           `json:"interface"`
	Variant   string           `json:"variant"`
	Level     int64            `json:"level"`
}
type PackageMetadata struct {
	packageinfo.Identity
	CompatVersion packageinfo.Version      `json:"compatVersion"`
	Name          packageinfo.Text         `json:"name,omitempty"`
	Description   packageinfo.Text         `json:"description,omitempty"`
	Vendor        packageinfo.Text         `json:"vendor,omitempty"`
	License       packageinfo.Text         `json:"license,omitempty"`
	Readme        packageinfo.Text         `json:"readme,omitempty"`
	URL           *string                  `json:"url,omitempty"`
	Dependencies  []packageinfo.Dependency `json:"dependencies"`
	Singers       []SingerSummary          `json:"singers"`
}
type RemoteManifest struct {
	RepositoryFormatVersion int `json:"repositoryFormatVersion"`
	PackageMetadata
	DownloadURL       string `json:"downloadUrl"`
	DownloadSha512    string `json:"downloadSha512"`
	DownloadSize      int64  `json:"downloadSize"`
	Deprecated        bool   `json:"deprecated,omitempty"`
	DeprecationReason string `json:"deprecationReason,omitempty"`
}
type VersionEntry struct {
	Version        packageinfo.Version `json:"version"`
	CompatVersion  packageinfo.Version `json:"compatVersion"`
	ManifestSha512 string              `json:"manifestSha512"`
	Deprecated     bool                `json:"deprecated"`
}
type VersionsIndex struct {
	RepositoryFormatVersion int                 `json:"repositoryFormatVersion"`
	ID                      string              `json:"id"`
	LatestVersion           packageinfo.Version `json:"latestVersion"`
	Versions                []VersionEntry      `json:"versions"`
}
type PackageEntry struct {
	ID                  string              `json:"id"`
	LatestVersion       packageinfo.Version `json:"latestVersion"`
	VersionsIndexSha512 string              `json:"versionsIndexSha512"`
}
type PackagesIndex struct {
	RepositoryFormatVersion int            `json:"repositoryFormatVersion"`
	RepositoryRevision      string         `json:"repositoryRevision"`
	Packages                []PackageEntry `json:"packages"`
}
type DecodeLimits struct{ MaxBytes int }
type DownloadURLMode int

const (
	StrictPublication DownloadURLMode = iota
	ClientUnrestrictedURLs
)

type ManifestDecodeOptions struct {
	Limits          DecodeLimits
	DownloadURLMode DownloadURLMode
}

var digestPattern = regexp.MustCompile(`^[0-9a-f]{128}$`)
var revisionPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func Sha512(raw []byte) string { d := sha512.Sum512(raw); return hex.EncodeToString(d[:]) }
func IsDigest(s string) bool   { return digestPattern.MatchString(s) }
func Marshal(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}
func MarshalManifest(m RemoteManifest) ([]byte, error) {
	b, err := Marshal(m)
	if err != nil {
		return nil, err
	}
	if _, err = DecodeManifest(b, ManifestDecodeOptions{}); err != nil {
		return nil, err
	}
	return b, nil
}

// Rejects invalid UTF-8 and unpaired escaped surrogates before JSON decoding.
// Returns nil for valid Unicode sequences. DecodeJSON checks the remaining JSON syntax.
func ValidateUnicode(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("The input contains an invalid UTF-8 byte sequence.")
	}
	in := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			in = !in
			continue
		}
		if !in || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return fmt.Errorf("The JSON input contains a truncated Unicode escape.")
		}
		var n uint16
		if _, err := fmt.Sscanf(string(raw[i+1:i+5]), "%04x", &n); err != nil {
			return err
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return fmt.Errorf("The JSON input contains an unpaired surrogate.")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return fmt.Errorf("The JSON input contains an unpaired surrogate.")
			}
			var lo uint16
			if _, err := fmt.Sscanf(string(raw[i+3:i+7]), "%04x", &lo); err != nil || lo < 0xdc00 || lo > 0xdfff {
				return fmt.Errorf("The JSON input contains an unpaired surrogate.")
			}
			i += 6
		}
	}
	return nil
}
func decode(raw []byte, max int, out any) (map[string]any, error) {
	if max == 0 {
		max = 1 << 20
	}
	if len(raw) > max {
		return nil, fmt.Errorf("The remote JSON input exceeds its byte budget.")
	}
	if err := ValidateUnicode(raw); err != nil {
		return nil, err
	}
	m, err := packageinfo.DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	if err = canonicalValues(m); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(out); err != nil {
		return nil, err
	}
	return m, nil
}
func canonicalValues(v any) error {
	switch x := v.(type) {
	case nil:
		return fmt.Errorf("Null values are not allowed.")
	case map[string]any:
		for _, v := range x {
			if err := canonicalValues(v); err != nil {
				return err
			}
		}
	case []any:
		for _, v := range x {
			if err := canonicalValues(v); err != nil {
				return err
			}
		}
	}
	return nil
}
func required(m map[string]any, keys ...string) error {
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("The required field %s is missing.", k)
		}
		if k == "version" || k == "compatVersion" || k == "latestVersion" {
			s, ok := m[k].(string)
			p, err := packageinfo.ParseVersion(s)
			if !ok || err != nil || len(s) > 128 || p.String() != s {
				return fmt.Errorf("The field %s has a noncanonical value.", k)
			}
		}
	}
	return nil
}
func validText(t packageinfo.Text) bool {
	if t == nil {
		return false
	}
	_, ok := t["_"]
	return ok
}
func checkTexts(m map[string]any) error {
	for _, k := range []string{"name", "vendor", "description", "license", "readme"} {
		if v, ok := m[k]; ok {
			obj, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("The field %s must be a Text object.", k)
			}
			if err := required(obj, "_"); err != nil {
				return err
			}
			for _, v := range obj {
				if _, ok := v.(string); !ok {
					return fmt.Errorf("The value of %s must be a string.", k)
				}
			}
		}
	}
	return nil
}
func ValidateURL(s string, unrestricted bool) error {
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(s, "\r\n\t ") {
		return fmt.Errorf("The download URL is invalid.")
	}
	if u.Scheme != "https" && !(unrestricted && u.Scheme == "http") {
		return fmt.Errorf("The download URL must use HTTPS.")
	}
	return nil
}
func PortablePath(s string) error {
	if !fs.ValidPath(s) || s == "." || len(s) > 220 || strings.ContainsAny(s, "\\:?#<>\"|*") {
		return fmt.Errorf("The portable path %q is unsafe.", s)
	}
	for _, p := range strings.Split(s, "/") {
		if strings.TrimRight(p, ". ") != p {
			return fmt.Errorf("The path is nonportable.")
		}
		for _, r := range p {
			if r < 32 || r == 127 {
				return fmt.Errorf("The path contains a control character.")
			}
		}
		b := strings.ToUpper(strings.SplitN(p, ".", 2)[0])
		if b == "CON" || b == "PRN" || b == "AUX" || b == "NUL" || ((strings.HasPrefix(b, "COM") || strings.HasPrefix(b, "LPT")) && len([]rune(b)) == 4 && strings.ContainsRune("123456789¹²³", []rune(b)[3])) {
			return fmt.Errorf("The path contains a reserved device name.")
		}
	}
	return nil
}
func validID(id string) bool {
	return len(id) <= 128 && len(strings.Split(id, "/")) <= 8 && packageinfo.IsValidPackageIdentifier(id) && PortablePath(id) == nil
}
func DecodeManifest(raw []byte, o ManifestDecodeOptions) (RemoteManifest, error) {
	var m RemoteManifest
	r, err := decode(raw, o.Limits.MaxBytes, &m)
	if err != nil {
		return m, err
	}
	if err = required(r, "repositoryFormatVersion", "id", "version", "compatVersion", "downloadUrl", "downloadSha512", "downloadSize", "dependencies", "singers"); err != nil {
		return m, err
	}
	if m.RepositoryFormatVersion != 1 {
		return m, fmt.Errorf("UNSUPPORTED_REPOSITORY_FORMAT: The repository format version is unsupported.")
	}
	if m.DownloadSize <= 0 {
		return m, fmt.Errorf("The download size must be a positive byte count.")
	}
	if !validID(m.ID) || m.CompatVersion.Compare(m.Version) > 0 || !IsDigest(m.DownloadSha512) {
		return m, fmt.Errorf("The identity, compatibility version, or digest is invalid.")
	}
	if err = ValidateURL(m.DownloadURL, o.DownloadURLMode == ClientUnrestrictedURLs); err != nil {
		return m, err
	}
	if err = checkTexts(r); err != nil {
		return m, err
	}
	if m.URL != nil && *m.URL != "" {
		if err = ValidateURL(*m.URL, true); err != nil {
			return m, err
		}
	}
	if m.Deprecated != (strings.TrimSpace(m.DeprecationReason) != "") {
		return m, fmt.Errorf("The deprecation reason must be present if and only if the version is deprecated.")
	}
	if !m.Deprecated {
		if _, ok := r["deprecationReason"]; ok {
			return m, fmt.Errorf("A version without deprecation must not contain a deprecation reason.")
		}
	}
	for _, t := range []packageinfo.Text{m.License, m.Readme} {
		for _, p := range t {
			if err = PortablePath(p); err != nil {
				return m, err
			}
		}
	}
	if len(m.Dependencies) > 1024 || len(m.Singers) > 1024 {
		return m, fmt.Errorf("The collection exceeds its resource budget.")
	}
	deps, ok := r["dependencies"].([]any)
	if !ok {
		return m, fmt.Errorf("The dependencies field must be an array.")
	}
	seen := map[string]bool{}
	for i, d := range m.Dependencies {
		obj := deps[i].(map[string]any)
		if err = required(obj, "id", "version"); err != nil {
			return m, err
		}
		if !validID(d.ID) || seen[d.ID] || d.ID == m.ID {
			return m, fmt.Errorf("A dependency is invalid, duplicate, or self-referential.")
		}
		seen[d.ID] = true
	}
	singers, ok := r["singers"].([]any)
	if !ok {
		return m, fmt.Errorf("The singers field must be an array.")
	}
	seen = map[string]bool{}
	for i, s := range m.Singers {
		obj := singers[i].(map[string]any)
		if err = required(obj, "id", "name", "interface", "variant", "level"); err != nil {
			return m, err
		}
		if err = checkTexts(obj); err != nil {
			return m, err
		}
		if !packageinfo.IsValidGenericIdentifier(s.ID) || seen[s.ID] || !packageinfo.ValidDottedID(s.Interface) || !packageinfo.ValidDottedID(s.Variant) || s.Level <= 0 {
			return m, fmt.Errorf("The singer summary is invalid.")
		}
		seen[s.ID] = true
	}
	return m, nil
}
func DecodeVersionsIndex(raw []byte, l DecodeLimits) (VersionsIndex, error) {
	var m VersionsIndex
	r, err := decode(raw, l.MaxBytes, &m)
	if err != nil {
		return m, err
	}
	if err = required(r, "repositoryFormatVersion", "id", "latestVersion", "versions"); err != nil {
		return m, err
	}
	if m.RepositoryFormatVersion != 1 || !validID(m.ID) || len(m.Versions) == 0 || m.LatestVersion.Compare(m.Versions[0].Version) != 0 {
		return m, fmt.Errorf("The versions index is invalid.")
	}
	for i, v := range m.Versions {
		obj := r["versions"].([]any)[i].(map[string]any)
		if err = required(obj, "version", "compatVersion", "manifestSha512", "deprecated"); err != nil {
			return m, err
		}
		if v.CompatVersion.Compare(v.Version) > 0 || !IsDigest(v.ManifestSha512) || i > 0 && m.Versions[i-1].Version.Compare(v.Version) <= 0 {
			return m, fmt.Errorf("The versions index contains an invalid entry or ordering.")
		}
	}
	return m, nil
}
func DecodePackagesIndex(raw []byte, l DecodeLimits) (PackagesIndex, error) {
	var m PackagesIndex
	r, err := decode(raw, l.MaxBytes, &m)
	if err != nil {
		return m, err
	}
	if err = required(r, "repositoryFormatVersion", "repositoryRevision", "packages"); err != nil {
		return m, err
	}
	if m.RepositoryFormatVersion != 1 || !revisionPattern.MatchString(m.RepositoryRevision) || m.Packages == nil {
		return m, fmt.Errorf("The packages index is invalid.")
	}
	for i, p := range m.Packages {
		obj := r["packages"].([]any)[i].(map[string]any)
		if err = required(obj, "id", "latestVersion", "versionsIndexSha512"); err != nil {
			return m, err
		}
		if !validID(p.ID) || !IsDigest(p.VersionsIndexSha512) || i > 0 && m.Packages[i-1].ID >= p.ID {
			return m, fmt.Errorf("The packages index contains an invalid entry or ordering.")
		}
	}
	return m, nil
}
