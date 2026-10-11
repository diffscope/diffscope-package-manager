package dependency

import (
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"strings"
	"testing"
)

func version(s string) packageinfo.Version {
	v, err := packageinfo.ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}
func candidate(id, v, compat string, deps ...packageinfo.Dependency) Candidate {
	return Candidate{packageinfo.Identity{ID: id, Version: version(v)}, version(compat), deps}
}
func dep(id, v string) packageinfo.Dependency {
	return packageinfo.Dependency{ID: id, Version: version(v)}
}
func TestCompatibilityAndNoBacktracking(t *testing.T) {
	all := []Candidate{candidate("root", "1", "1", dep("base", "1")), candidate("base", "1", "1"), candidate("base", "2", "1", dep("missing", "1"))}
	if err := Validate(all); err == nil || !strings.Contains(err.Error(), "root=1.0.0.0") && !strings.Contains(err.Error(), "base=2.0.0.0 -> missing=1.0.0.0") {
		t.Fatal(err)
	}
	selected, ok := Select(all, dep("base", "1"))
	if !ok || selected.Version.Compare(version("2")) != 0 {
		t.Fatal("did not choose highest compatible")
	}
	all[2].Dependencies = nil
	if err := Validate(all); err != nil {
		t.Fatal(err)
	}
	if _, ok := Select(all, dep("base", "3")); ok {
		t.Fatal("accepted too-new target")
	}
}
func TestCycleAndRemovalReselection(t *testing.T) {
	cycle := []Candidate{candidate("a", "1", "1", dep("b", "1")), candidate("b", "1", "1", dep("a", "1"))}
	if err := Validate(cycle); err == nil || !strings.Contains(err.Error(), "a=1.0.0.0 -> b=1.0.0.0 -> a=1.0.0.0") {
		t.Fatal(err)
	}
	all := []Candidate{candidate("voice", "1", "1", dep("base", "1")), candidate("base", "1", "1"), candidate("base", "2", "1")}
	removed := Cascade(all, map[string]bool{"base=2.0.0.0": true})
	if removed["voice=1.0.0.0"] {
		t.Fatal("removed a valid dependent despite alternative")
	}
	removed = Cascade(all, map[string]bool{"base=1.0.0.0": true, "base=2.0.0.0": true})
	if !removed["voice=1.0.0.0"] {
		t.Fatal("missing dependent")
	}
}
