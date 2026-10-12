// Package packagedependency computes deterministic package graphs without I/O.
package packagedependency

import (
	"context"
	"errors"
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
type Edge struct {
	Requester packageinfo.Identity   `json:"requester"`
	Required  packageinfo.Dependency `json:"required"`
	Provider  packageinfo.Identity   `json:"provider"`
}
type Graph struct {
	Roots []packageinfo.Identity `json:"roots"`
	Nodes []Candidate            `json:"nodes"`
	Edges []Edge                 `json:"edges"`
}
type Limits struct {
	MaxCandidates, MaxEdges int
	MaxValueBytes           int64
}

func DefaultLimits() Limits { return Limits{10000, 100000, 64 << 20} }

type Error struct {
	Kind            string
	Root, Requester packageinfo.Identity
	Required        packageinfo.Dependency
	Chain           []string
	Reason          string
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
func less(a, b Candidate) bool {
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Version.Compare(b.Version) < 0
}
func ValidateAll(ctx context.Context, all []Candidate, limits Limits) (Graph, error) {
	roots := make([]packageinfo.Identity, len(all))
	for i, c := range all {
		roots[i] = c.Identity
	}
	return ResolveRoots(ctx, all, roots, limits)
}
func ResolveRoots(ctx context.Context, all []Candidate, roots []packageinfo.Identity, limits Limits) (Graph, error) {
	g := Graph{Roots: append([]packageinfo.Identity{}, roots...), Nodes: []Candidate{}, Edges: []Edge{}}
	if limits.MaxCandidates == 0 {
		limits = DefaultLimits()
	}
	if len(all) > limits.MaxCandidates {
		return g, fmt.Errorf("The candidate count exceeds its budget.")
	}
	byID := map[string][]Candidate{}
	byKey := map[string]Candidate{}
	var cost int64
	for _, c := range all {
		if _, ok := byKey[c.String()]; ok {
			return g, fmt.Errorf("The package identity %s is duplicate.", c.String())
		}
		byKey[c.String()] = c
		byID[c.ID] = append(byID[c.ID], c)
		cost += int64(len(c.String()) + len(c.CompatVersion.String()))
		for _, d := range c.Dependencies {
			cost += int64(len(d.ID) + len(d.Version.String()))
		}
	}
	if cost > limits.MaxValueBytes {
		return g, fmt.Errorf("The dependency metadata exceeds its byte budget.")
	}
	sort.Slice(g.Roots, func(i, j int) bool { return less(byKey[g.Roots[i].String()], byKey[g.Roots[j].String()]) })
	color := map[string]int{}
	type frame struct {
		c     Candidate
		edges []Edge
		next  int
	}
	for _, root := range g.Roots {
		if err := ctx.Err(); err != nil {
			return g, err
		}
		c, ok := byKey[root.String()]
		if !ok {
			return g, fmt.Errorf("The root package %s is missing.", root.String())
		}
		if color[c.String()] == 2 {
			continue
		}
		stack := []frame{{c: c}}
		for len(stack) > 0 {
			if err := ctx.Err(); err != nil {
				return g, err
			}
			f := &stack[len(stack)-1]
			key := f.c.String()
			fail := func(kind, reason string, required packageinfo.Dependency) error {
				chain := []string{}
				for _, v := range stack {
					chain = append(chain, v.c.String())
				}
				if kind == "MISSING_DEPENDENCY" {
					chain = append(chain, required.ID+"="+required.Version.String())
				}
				if kind == "DEPENDENCY_CYCLE" {
					chain = append(chain, strings.TrimPrefix(reason, "The dependency cycle includes "))
				}
				return &Error{Kind: kind, Root: root, Requester: f.c.Identity, Required: required, Chain: chain, Reason: reason + "."}
			}
			if color[key] == 0 {
				color[key] = 1
				g.Nodes = append(g.Nodes, f.c)
				seen := map[string]bool{}
				deps := append([]packageinfo.Dependency{}, f.c.Dependencies...)
				sort.Slice(deps, func(i, j int) bool { return deps[i].ID < deps[j].ID })
				for _, d := range deps {
					if seen[d.ID] || d.ID == f.c.ID {
						return g, fail("INVALID_DEPENDENCY", "The dependency is duplicate or self-referential: "+d.ID, d)
					}
					seen[d.ID] = true
					p, ok := Select(byID[d.ID], d)
					if !ok {
						return g, fail("MISSING_DEPENDENCY", "No compatible provider is available for "+d.ID+"="+d.Version.String(), d)
					}
					e := Edge{f.c.Identity, d, p.Identity}
					f.edges = append(f.edges, e)
					g.Edges = append(g.Edges, e)
					if len(g.Edges) > limits.MaxEdges {
						return g, fmt.Errorf("The dependency edge count exceeds its budget.")
					}
				}
			}
			if f.next == len(f.edges) {
				color[key] = 2
				stack = stack[:len(stack)-1]
				continue
			}
			e := f.edges[f.next]
			f.next++
			if color[e.Provider.String()] == 1 {
				return g, fail("DEPENDENCY_CYCLE", "The dependency cycle includes "+e.Provider.String(), e.Required)
			}
			if color[e.Provider.String()] == 0 {
				stack = append(stack, frame{c: byKey[e.Provider.String()]})
			}
		}
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return less(g.Nodes[i], g.Nodes[j]) })
	sort.Slice(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.Requester.String() != b.Requester.String() {
			return a.Requester.String() < b.Requester.String()
		}
		return a.Required.ID < b.Required.ID
	})
	return g, nil
}
func Cascade(ctx context.Context, all []Candidate, remove []packageinfo.Identity, limits Limits) ([]packageinfo.Identity, error) {
	removed := map[string]packageinfo.Identity{}
	for _, id := range remove {
		removed[id.String()] = id
	}
	for {
		remaining := []Candidate{}
		for _, c := range all {
			if _, ok := removed[c.String()]; !ok {
				remaining = append(remaining, c)
			}
		}
		changed := false
		for _, c := range remaining {
			if _, err := ResolveRoots(ctx, remaining, []packageinfo.Identity{c.Identity}, limits); err != nil {
				var graphError *Error
				if !errors.As(err, &graphError) {
					return nil, err
				}
				removed[c.String()] = c.Identity
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	out := []packageinfo.Identity{}
	for _, id := range removed {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}
