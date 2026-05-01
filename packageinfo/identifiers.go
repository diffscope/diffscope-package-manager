package packageinfo

import (
	"fmt"
	"regexp"
)

var (
	packageIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*$`)
	genericIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ParsePackageIdentifier validates and returns a package identifier.
func ParsePackageIdentifier(text string) (string, error) {
	if !IsValidPackageIdentifier(text) {
		return "", fmt.Errorf("invalid package identifier %q", text)
	}
	return text, nil
}

// ParseGenericIdentifier validates and returns a generic identifier.
func ParseGenericIdentifier(text string) (string, error) {
	if !IsValidGenericIdentifier(text) {
		return "", fmt.Errorf("invalid generic identifier %q", text)
	}
	return text, nil
}

// IsValidPackageIdentifier reports whether the input matches the package identifier format.
func IsValidPackageIdentifier(text string) bool {
	if text == "" {
		return false
	}
	return packageIdentifierPattern.MatchString(text)
}

// IsValidGenericIdentifier reports whether the input matches the generic identifier format.
func IsValidGenericIdentifier(text string) bool {
	if text == "" {
		return false
	}
	return genericIdentifierPattern.MatchString(text)
}
