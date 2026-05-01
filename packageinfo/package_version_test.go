package packageinfo

import (
	"testing"

	"github.com/go-playground/validator/v10"
)

func TestParseAndString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    PackageVersion
		wantStr string
	}{
		{name: "single", input: "1", want: PackageVersion{Major: 1}, wantStr: "1.0.0.0"},
		{name: "two segments", input: "1.2", want: PackageVersion{Major: 1, Minor: 2}, wantStr: "1.2.0.0"},
		{name: "four segments", input: "1.2.3.4", want: PackageVersion{Major: 1, Minor: 2, Patch: 3, Build: 4}, wantStr: "1.2.3.4"},
		{name: "keep trailing zeros", input: "1.0.0.0", want: PackageVersion{Major: 1}, wantStr: "1.0.0.0"},
		{name: "keep middle and trailing zero", input: "1.0.2.0", want: PackageVersion{Major: 1, Minor: 0, Patch: 2}, wantStr: "1.0.2.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePackageVersion(tt.input)
			if err != nil {
				t.Fatalf("ParsePackageVersion() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParsePackageVersion() = %#v, want %#v", got, tt.want)
			}
			if got.String() != tt.wantStr {
				t.Fatalf("String() = %q, want %q", got.String(), tt.wantStr)
			}
		})
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	inputs := []string{"", "1.2.3.4.5", "01", "1.", ".1", "1.2.-3", "1.2.3a"}
	for _, input := range inputs {
		if _, err := ParsePackageVersion(input); err == nil {
			t.Fatalf("ParsePackageVersion(%q) succeeded unexpectedly", input)
		}
	}
}

func TestCompare(t *testing.T) {
	base := MustParsePackageVersion("1.2.3")
	if got := base.Compare(MustParsePackageVersion("1.2.3")); got != 0 {
		t.Fatalf("Compare equal = %d, want 0", got)
	}
	if got := base.Compare(MustParsePackageVersion("1.2.4")); got != -1 {
		t.Fatalf("Compare smaller = %d, want -1", got)
	}
	if got := base.Compare(MustParsePackageVersion("1.2.2")); got != 1 {
		t.Fatalf("Compare greater = %d, want 1", got)
	}
}

func TestValidator(t *testing.T) {
	v := validator.New()
	if err := RegisterValidator(v); err != nil {
		t.Fatalf("RegisterValidator() error = %v", err)
	}

	type sample struct {
		Text     string         `validate:"dspm_package_version"`
		Value    PackageVersion `validate:"dspm_package_version"`
		Package  string         `validate:"dspm_package_id"`
		Generic  string         `validate:"dspm_generic_id"`
		Optional *string        `validate:"dspm_generic_id"`
	}

	valid := "singer_01"
	if err := v.Struct(sample{
		Text:     "1.2.3",
		Value:    MustParsePackageVersion("1.2.3"),
		Package:  "foo/bar",
		Generic:  "inference_01",
		Optional: &valid,
	}); err != nil {
		t.Fatalf("valid struct rejected: %v", err)
	}
	if err := v.Struct(sample{Text: "1.02", Value: MustParsePackageVersion("1.2.3"), Package: "foo/bar", Generic: "ok"}); err == nil {
		t.Fatal("invalid version text accepted unexpectedly")
	}
}
