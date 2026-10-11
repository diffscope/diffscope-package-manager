package packageinfo

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Version has four arbitrary-precision decimal components. Its zero value is 0.
type Version struct{ parts [4]string }

func ParseVersion(s string) (Version, error) {
	var v Version
	p := strings.Split(s, ".")
	if len(p) > 4 {
		return v, fmt.Errorf("invalid version %q", s)
	}
	for i, c := range p {
		if c == "" || (len(c) > 1 && c[0] == '0') {
			return v, fmt.Errorf("invalid version %q", s)
		}
		for _, b := range []byte(c) {
			if b < '0' || b > '9' {
				return v, fmt.Errorf("invalid version %q", s)
			}
		}
		v.parts[i] = c
	}
	return v, nil
}
func (v Version) components() [4]string {
	p := v.parts
	for i := range p {
		if p[i] == "" {
			p[i] = "0"
		}
	}
	return p
}
func (v Version) String() string { p := v.components(); return strings.Join(p[:], ".") }
func (v Version) Compare(w Version) int {
	a, b := v.components(), w.components()
	for i := range a {
		if len(a[i]) < len(b[i]) {
			return -1
		}
		if len(a[i]) > len(b[i]) {
			return 1
		}
		if c := strings.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
func (v Version) MarshalJSON() ([]byte, error) { return json.Marshal(v.String()) }
func (v *Version) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	w, err := ParseVersion(s)
	if err == nil {
		*v = w
	}
	return err
}

type Identity struct {
	ID      string  `json:"id"`
	Version Version `json:"version"`
}

func (i Identity) String() string { return i.ID + "=" + i.Version.String() }

// Selector is a CLI target, not a persisted ModuleReference.
type Selector struct {
	ID             string
	Version        *Version
	Category       string
	ContributionID string
}

func ValidDottedID(s string) bool {
	for _, p := range strings.Split(s, ".") {
		if !IsValidGenericIdentifier(p) {
			return false
		}
	}
	return true
}
func ParseSelector(s string) (Selector, error) {
	var t Selector
	base, contrib, hasContrib := strings.Cut(s, ":")
	id, version, hasVersion := strings.Cut(base, "=")
	if !IsValidPackageIdentifier(id) {
		return t, fmt.Errorf("invalid package selector %q", s)
	}
	t.ID = id
	if hasVersion {
		v, err := ParseVersion(version)
		if err != nil {
			return t, err
		}
		t.Version = &v
	}
	if hasContrib {
		c, i, ok := strings.Cut(contrib, "/")
		if !ok || !ValidDottedID(c) || !IsValidGenericIdentifier(i) {
			return t, fmt.Errorf("invalid contribution selector %q", s)
		}
		t.Category = c
		t.ContributionID = i
	}
	return t, nil
}
