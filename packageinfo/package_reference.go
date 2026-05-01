package packageinfo

import (
	"fmt"
	"strings"
)

// PackageReferenceType describes the parsed query target.
type PackageReferenceType int

const (
	PackageReferenceTypePackage PackageReferenceType = iota
	PackageReferenceTypeInference
	PackageReferenceTypeSinger
)

// PackageReference represents a parsed package query string.
type PackageReference struct {
	Type        PackageReferenceType
	PackageID   string
	Version     *PackageVersion
	InferenceID string
	SingerID    string
}

// ParsePackageReference parses package@version, package@version:inference, and package@version[singer].
// The version is optional; when omitted, Version is nil.
func ParsePackageReference(text string) (PackageReference, error) {
	if text == "" {
		return PackageReference{}, fmt.Errorf("package query cannot be empty")
	}

	queryType := PackageReferenceTypePackage
	base := text
	var inferenceID string
	var singerID string

	colonIndex := strings.Index(text, ":")
	bracketIndex := strings.Index(text, "[")

	if colonIndex >= 0 && bracketIndex >= 0 {
		return PackageReference{}, fmt.Errorf("package query %q mixes inference and singer selectors", text)
	}

	if colonIndex >= 0 {
		queryType = PackageReferenceTypeInference
		base = text[:colonIndex]
		inferenceID = text[colonIndex+1:]
		if inferenceID == "" {
			return PackageReference{}, fmt.Errorf("package query %q missing inference identifier", text)
		}
		if _, err := ParseGenericIdentifier(inferenceID); err != nil {
			return PackageReference{}, err
		}
	} else if bracketIndex >= 0 {
		if !strings.HasSuffix(text, "]") {
			return PackageReference{}, fmt.Errorf("package query %q has unterminated singer selector", text)
		}
		queryType = PackageReferenceTypeSinger
		base = text[:bracketIndex]
		singerID = text[bracketIndex+1 : len(text)-1]
		if singerID == "" {
			return PackageReference{}, fmt.Errorf("package query %q missing singer identifier", text)
		}
		if _, err := ParseGenericIdentifier(singerID); err != nil {
			return PackageReference{}, err
		}
	}

	packageID, version, err := parsePackageBase(base)
	if err != nil {
		return PackageReference{}, err
	}

	return PackageReference{
		Type:        queryType,
		PackageID:   packageID,
		Version:     version,
		InferenceID: inferenceID,
		SingerID:    singerID,
	}, nil
}

// MustParsePackageReference parses a package query or panics on error.
func MustParsePackageReference(text string) PackageReference {
	query, err := ParsePackageReference(text)
	if err != nil {
		panic(err)
	}
	return query
}

// IsValidPackageReference reports whether the input is a valid package query string.
func IsValidPackageReference(text string) bool {
	_, err := ParsePackageReference(text)
	return err == nil
}

// String returns the canonical textual representation of the package query.
func (q PackageReference) String() string {
	base := q.PackageID
	if q.PackageID != "" && q.Version != nil {
		base = base + "@" + q.Version.String()
	}

	switch q.Type {
	case PackageReferenceTypeInference:
		if q.InferenceID == "" {
			return base
		}
		return base + ":" + q.InferenceID
	case PackageReferenceTypeSinger:
		if q.SingerID == "" {
			return base
		}
		return base + "[" + q.SingerID + "]"
	default:
		return base
	}
}

func parsePackageBase(text string) (string, *PackageVersion, error) {
	if text == "" {
		return "", nil, nil
	}

	parts := strings.Split(text, "@")
	if len(parts) > 2 {
		return "", nil, fmt.Errorf("package query %q contains multiple @", text)
	}
	if parts[0] == "" {
		return "", nil, fmt.Errorf("package query %q cannot specify version without package identifier", text)
	}

	packageID, err := ParsePackageIdentifier(parts[0])
	if err != nil {
		return "", nil, err
	}

	if len(parts) == 1 || parts[1] == "" {
		return packageID, nil, nil
	}

	parsed, err := ParsePackageVersion(parts[1])
	if err != nil {
		return "", nil, err
	}
	return packageID, &parsed, nil
}
