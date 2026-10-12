package packageinfo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Version contains four nonnegative integer components. Its zero value is 0.0.0.0.
type Version struct{ parts [4]int }

func ParseVersion(s string) (Version, error) {
	var v Version
	p := strings.Split(s, ".")
	if len(p) > 4 {
		return v, fmt.Errorf("The version %q is invalid.", s)
	}
	for i, c := range p {
		if c == "" || c[0] < '0' || c[0] > '9' || (len(c) > 1 && c[0] == '0') {
			return v, fmt.Errorf("The version %q is invalid.", s)
		}
		n, err := strconv.Atoi(c)
		if err != nil {
			return v, fmt.Errorf("The version %q is invalid.", s)
		}
		v.parts[i] = n
	}
	return v, nil
}
func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d.%d", v.parts[0], v.parts[1], v.parts[2], v.parts[3])
}
func (v Version) Compare(w Version) int {
	for i, a := range v.parts {
		if a < w.parts[i] {
			return -1
		}
		if a > w.parts[i] {
			return 1
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
		return t, fmt.Errorf("The package selector %q is invalid.", s)
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
			return t, fmt.Errorf("The contribution selector %q is invalid.", s)
		}
		t.Category = c
		t.ContributionID = i
	}
	return t, nil
}
