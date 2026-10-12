package packageinfo

import (
	"fmt"
	"regexp"
)

var (
	packageIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*$`)
	genericIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Returns the package identifier or an error if its syntax is invalid.
func ParsePackageIdentifier(text string) (string, error) {
	if !IsValidPackageIdentifier(text) {
		return "", fmt.Errorf("The package identifier %q is invalid.", text)
	}
	return text, nil
}

// Returns the generic identifier or an error if its syntax is invalid.
func ParseGenericIdentifier(text string) (string, error) {
	if !IsValidGenericIdentifier(text) {
		return "", fmt.Errorf("The generic identifier %q is invalid.", text)
	}
	return text, nil
}

// Returns whether the input matches the package identifier format.
func IsValidPackageIdentifier(text string) bool {
	if text == "" {
		return false
	}
	return packageIdentifierPattern.MatchString(text)
}

// Returns whether the input matches the generic identifier format.
func IsValidGenericIdentifier(text string) bool {
	if text == "" {
		return false
	}
	return genericIdentifierPattern.MatchString(text)
}
