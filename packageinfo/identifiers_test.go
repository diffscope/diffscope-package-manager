package packageinfo

import "testing"

func TestPackageIdentifierValidation(t *testing.T) {
	valid := []string{"foo", "foo/bar", "foo_bar/baz-1", "A1-_/b"}
	for _, input := range valid {
		if _, err := ParsePackageIdentifier(input); err != nil {
			t.Fatalf("ParsePackageIdentifier(%q) error = %v", input, err)
		}
	}

	invalid := []string{"", "foo/", "/bar", "foo//bar", "foo:bar", "foo bar"}
	for _, input := range invalid {
		if IsValidPackageIdentifier(input) {
			t.Fatalf("IsValidPackageIdentifier(%q) returned true", input)
		}
	}
}

func TestGenericIdentifierValidation(t *testing.T) {
	valid := []string{"foo", "Foo_1", "bar-2"}
	for _, input := range valid {
		if _, err := ParseGenericIdentifier(input); err != nil {
			t.Fatalf("ParseGenericIdentifier(%q) error = %v", input, err)
		}
	}

	invalid := []string{"", "foo/bar", "foo:bar", "foo bar"}
	for _, input := range invalid {
		if IsValidGenericIdentifier(input) {
			t.Fatalf("IsValidGenericIdentifier(%q) returned true", input)
		}
	}
}
