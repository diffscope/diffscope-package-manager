package packagedependency

import (
	"context"
	"errors"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"testing"
)

func TestRootClosureAndFullGraphBoundary(t *testing.T) {
	v, _ := packageinfo.ParseVersion("1")
	good := Candidate{Identity: packageinfo.Identity{ID: "good", Version: v}, CompatVersion: v}
	bad := Candidate{Identity: packageinfo.Identity{ID: "bad", Version: v}, CompatVersion: v, Dependencies: []packageinfo.Dependency{{ID: "missing", Version: v}}}
	all := []Candidate{good, bad}
	if _, err := ResolveRoots(context.Background(), all, []packageinfo.Identity{good.Identity}, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateAll(context.Background(), all, DefaultLimits()); err == nil {
		t.Fatal("unreachable bad root not checked")
	}
}

func TestCascadePropagatesCancellationAndLimits(t *testing.T) {
	v, _ := packageinfo.ParseVersion("1")
	all := []Candidate{
		{Identity: packageinfo.Identity{ID: "base", Version: v}, CompatVersion: v},
		{Identity: packageinfo.Identity{ID: "voice", Version: v}, CompatVersion: v, Dependencies: []packageinfo.Dependency{{ID: "base", Version: v}}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if removed, err := Cascade(ctx, all, nil, DefaultLimits()); !errors.Is(err, context.Canceled) || removed != nil {
		t.Fatal("Cancellation produced a removal plan.", removed, err)
	}
	limits := DefaultLimits()
	limits.MaxCandidates = 1
	if removed, err := Cascade(context.Background(), all, nil, limits); err == nil || removed != nil {
		t.Fatal("An exceeded budget produced a removal plan.", removed, err)
	}
}
