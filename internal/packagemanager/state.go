package packagemanager

import (
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	dependency "github.com/diffscope/diffscope-package-manager/packagedependency"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

func mapRecords(m map[string]packagedatabase.Record) []packagedatabase.Record {
	r := make([]packagedatabase.Record, 0, len(m))
	for _, v := range m {
		r = append(r, v)
	}
	return r
}
func candidates(records []packagedatabase.Record) []dependency.Candidate {
	r := make([]dependency.Candidate, 0, len(records))
	for _, p := range records {
		r = append(r, dependency.Candidate{Identity: p.Identity, CompatVersion: p.CompatVersion, Dependencies: p.Dependencies})
	}
	return r
}
func resolve(records []packagedatabase.Record, selector packageinfo.Selector) (packagedatabase.Record, error) {
	matches := []packagedatabase.Record{}
	for _, p := range records {
		if p.ID == selector.ID && (selector.Version == nil || p.Version.Compare(*selector.Version) == 0) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		return packagedatabase.Record{}, failure("NOT_FOUND", fmt.Errorf("The package %s is not installed.", selector.ID))
	}
	if len(matches) > 1 {
		ids := []string{}
		for _, p := range matches {
			ids = append(ids, p.String())
		}
		return packagedatabase.Record{}, &Error{Code: "AMBIGUOUS_VERSION", Message: "Multiple versions are installed. An explicit version is required.", Details: ids}
	}
	return matches[0], nil
}
