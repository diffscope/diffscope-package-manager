package packageinfo

import "testing"

func TestParsePackageReferencePackage(t *testing.T) {
	query, err := ParsePackageReference("foo/bar@1.2.3")
	if err != nil {
		t.Fatalf("ParsePackageReference() error = %v", err)
	}
	if query.Type != PackageReferenceTypePackage {
		t.Fatalf("Type = %v, want package", query.Type)
	}
	if query.PackageID != "foo/bar" {
		t.Fatalf("PackageID = %q", query.PackageID)
	}
	if query.Version == nil || query.Version.String() != "1.2.3.0" {
		t.Fatalf("Version = %#v", query.Version)
	}
}

func TestParsePackageReferenceInference(t *testing.T) {
	query, err := ParsePackageReference("foo@1.2:inf")
	if err != nil {
		t.Fatalf("ParsePackageReference() error = %v", err)
	}
	if query.Type != PackageReferenceTypeInference {
		t.Fatalf("Type = %v, want inference", query.Type)
	}
	if query.InferenceID != "inf" {
		t.Fatalf("InferenceID = %q", query.InferenceID)
	}
}

func TestParsePackageReferenceSingerWithoutVersion(t *testing.T) {
	query, err := ParsePackageReference("foo/bar[singer]")
	if err != nil {
		t.Fatalf("ParsePackageReference() error = %v", err)
	}
	if query.Type != PackageReferenceTypeSinger {
		t.Fatalf("Type = %v, want singer", query.Type)
	}
	if query.Version != nil {
		t.Fatalf("Version expected nil, got %#v", query.Version)
	}
	if query.SingerID != "singer" {
		t.Fatalf("SingerID = %q", query.SingerID)
	}
}

func TestParsePackageReferenceInferenceWithoutPackageID(t *testing.T) {
	query, err := ParsePackageReference(":foo")
	if err != nil {
		t.Fatalf("ParsePackageReference() error = %v", err)
	}
	if query.Type != PackageReferenceTypeInference {
		t.Fatalf("Type = %v, want inference", query.Type)
	}
	if query.PackageID != "" {
		t.Fatalf("PackageID = %q, want empty", query.PackageID)
	}
	if query.Version != nil {
		t.Fatalf("Version expected nil, got %#v", query.Version)
	}
	if query.InferenceID != "foo" {
		t.Fatalf("InferenceID = %q", query.InferenceID)
	}
}

func TestParsePackageReferenceSingerWithoutPackageID(t *testing.T) {
	query, err := ParsePackageReference("[bar]")
	if err != nil {
		t.Fatalf("ParsePackageReference() error = %v", err)
	}
	if query.Type != PackageReferenceTypeSinger {
		t.Fatalf("Type = %v, want singer", query.Type)
	}
	if query.PackageID != "" {
		t.Fatalf("PackageID = %q, want empty", query.PackageID)
	}
	if query.Version != nil {
		t.Fatalf("Version expected nil, got %#v", query.Version)
	}
	if query.SingerID != "bar" {
		t.Fatalf("SingerID = %q", query.SingerID)
	}
}

func TestParsePackageReferenceRejectVersionWithoutPackageID(t *testing.T) {
	if _, err := ParsePackageReference("@1.0:foo"); err == nil {
		t.Fatal("expected @1.0:foo to be invalid")
	}
	if _, err := ParsePackageReference("@1.0[bar]"); err == nil {
		t.Fatal("expected @1.0[bar] to be invalid")
	}
}

func TestPackageReferenceString(t *testing.T) {
	query := MustParsePackageReference("foo/bar@1.2.3:inf")
	if query.String() != "foo/bar@1.2.3.0:inf" {
		t.Fatalf("String() = %q", query.String())
	}

	plain := MustParsePackageReference("foo/bar")
	if plain.String() != "foo/bar" {
		t.Fatalf("String() = %q", plain.String())
	}

	onlyInference := MustParsePackageReference(":foo")
	if onlyInference.String() != ":foo" {
		t.Fatalf("String() = %q", onlyInference.String())
	}

	onlySinger := MustParsePackageReference("[bar]")
	if onlySinger.String() != "[bar]" {
		t.Fatalf("String() = %q", onlySinger.String())
	}
}

func TestIsValidPackageReference(t *testing.T) {
	if !IsValidPackageReference("foo/bar@1.2.3[singer]") {
		t.Fatal("expected query to be valid")
	}
	if IsValidPackageReference("foo@1.2:inf[singer]") {
		t.Fatal("expected mixed selector query to be invalid")
	}
}
