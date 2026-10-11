// Package dependency computes package graphs without I/O or CLI dependencies.
package dependency

import (
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"sort"
	"strings"
)

type Candidate struct {
	packageinfo.Identity
	CompatVersion packageinfo.Version
	Dependencies  []packageinfo.Dependency
}
type Error struct {
	Chain  []string
	Reason string
}

func (e *Error) Error() string { return strings.Join(e.Chain, " -> ") + ": " + e.Reason }

func Select(all []Candidate, dep packageinfo.Dependency) (Candidate, bool) {
	var best Candidate
	found := false
	for _, c := range all {
		if c.ID == dep.ID && c.CompatVersion.Compare(dep.Version) <= 0 && c.Version.Compare(dep.Version) >= 0 && (!found || c.Version.Compare(best.Version) > 0) {
			best = c
			found = true
		}
	}
	return best, found
}
func Validate(all []Candidate) error {
	sorted := append([]Candidate(nil), all...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	seen := map[string]bool{}
	for _, c := range sorted {
		if seen[c.String()] {
			return fmt.Errorf("duplicate package identity %s", c.String())
		}
		seen[c.String()] = true
		if err := validateRoot(all, c); err != nil {
			return err
		}
	}
	return nil
}
func validateRoot(all []Candidate, root Candidate) error {
	visited := map[string]bool{}
	active := map[string]bool{}
	var walk func(Candidate, []string) error
	walk = func(c Candidate, chain []string) error {
		key := c.String()
		chain = append(append([]string(nil), chain...), key)
		if active[key] {
			return &Error{chain, "dependency cycle"}
		}
		if visited[key] {
			return nil
		}
		active[key] = true
		defer delete(active, key)
		seen := map[string]bool{}
		for _, dep := range c.Dependencies {
			if seen[dep.ID] || dep.ID == c.ID {
				return &Error{chain, "duplicate or self dependency " + dep.ID}
			}
			seen[dep.ID] = true
			next, ok := Select(all, dep)
			if !ok {
				return &Error{append(chain, dep.ID+"="+dep.Version.String()), "no compatible installed package"}
			}
			if err := walk(next, chain); err != nil {
				return err
			}
		}
		visited[key] = true
		return nil
	}
	return walk(root, nil)
}

// Cascade removes roots whose selected dependency closure is no longer valid.
func Cascade(all []Candidate, remove map[string]bool) map[string]bool {
	result := map[string]bool{}
	for k, v := range remove {
		result[k] = v
	}
	for {
		remaining := []Candidate{}
		for _, c := range all {
			if !result[c.String()] {
				remaining = append(remaining, c)
			}
		}
		changed := false
		for _, c := range remaining {
			if validateRoot(remaining, c) != nil {
				result[c.String()] = true
				changed = true
			}
		}
		if !changed {
			return result
		}
	}
}
