package packageinfo

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PackageVersion represents a DiffScope package version with up to four numeric segments.
type PackageVersion struct {
	Major uint64
	Minor uint64
	Patch uint64
	Build uint64
}

// ParsePackageVersion converts a textual version into a PackageVersion.
func ParsePackageVersion(text string) (PackageVersion, error) {
	if text == "" {
		return PackageVersion{}, errors.New("package version cannot be empty")
	}

	parts := strings.Split(text, ".")
	if len(parts) < 1 || len(parts) > 4 {
		return PackageVersion{}, fmt.Errorf("invalid package version %q", text)
	}

	values := [4]uint64{}
	for index, part := range parts {
		value, err := parseVersionComponent(part)
		if err != nil {
			return PackageVersion{}, fmt.Errorf("invalid package version %q: %w", text, err)
		}
		values[index] = value
	}

	return PackageVersion{
		Major: values[0],
		Minor: values[1],
		Patch: values[2],
		Build: values[3],
	}, nil
}

// MustParsePackageVersion converts a textual version into a PackageVersion and panics on error.
func MustParsePackageVersion(text string) PackageVersion {
	version, err := ParsePackageVersion(text)
	if err != nil {
		panic(err)
	}
	return version
}

// IsValidPackageVersion reports whether the given text is a valid package version string.
func IsValidPackageVersion(text string) bool {
	_, err := ParsePackageVersion(text)
	return err == nil
}

// Compare compares two package versions.
// It returns -1 if v is smaller than other, 0 if equal, and 1 if greater.
func (v PackageVersion) Compare(other PackageVersion) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}
	if v.Build != other.Build {
		if v.Build < other.Build {
			return -1
		}
		return 1
	}
	return 0
}

// String returns the canonical textual representation of the package version.
func (v PackageVersion) String() string {
	segments := []uint64{v.Major, v.Minor, v.Patch, v.Build}
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		parts = append(parts, strconv.FormatUint(segment, 10))
	}
	return strings.Join(parts, ".")
}

// MarshalText implements encoding.TextMarshaler.
func (v PackageVersion) MarshalText() ([]byte, error) {
	return []byte(v.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *PackageVersion) UnmarshalText(text []byte) error {
	if v == nil {
		return errors.New("packageinfo: nil receiver")
	}

	parsed, err := ParsePackageVersion(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

func parseVersionComponent(part string) (uint64, error) {
	if part == "" {
		return 0, errors.New("empty version component")
	}
	if len(part) > 1 && part[0] == '0' {
		return 0, fmt.Errorf("leading zeros are not allowed in %q", part)
	}

	value, err := strconv.ParseUint(part, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid numeric component %q", part)
	}
	return value, nil
}
