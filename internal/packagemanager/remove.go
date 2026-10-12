package packagemanager

import (
	"context"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/localstore"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	dependency "github.com/diffscope/diffscope-package-manager/packagedependency"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

type RemoveRequest struct {
	Targets                        []string
	Cascade, IgnoreMissing, DryRun bool
}

func (m *Manager) Remove(ctx context.Context, request RemoveRequest, observer Observer) (MutationResult, error) {
	result := MutationResult{Actions: []Action{}, DryRun: request.DryRun}
	_, resolved := beginProgress(observer, "resolve", m.Root)
	s, err := localstore.Open(m.Root, !request.DryRun)
	if err != nil {
		return result, err
	}
	defer s.Close()
	result.Diagnostics = s.Diagnostics
	records, err := s.Records()
	if err != nil {
		return result, err
	}
	selected := map[string]bool{}
	for _, target := range request.Targets {
		selector, err := packageinfo.ParseSelector(target)
		if err != nil || selector.Category != "" {
			return result, failure("INVALID_ARGUMENT", fmt.Errorf("The removal target %q is invalid.", target))
		}
		p, err := resolve(records, selector)
		if err != nil {
			var e *Error
			if request.IgnoreMissing && errors.As(err, &e) && e.Code == "NOT_FOUND" {
				result.Ignored = append(result.Ignored, target)
				continue
			}
			return result, err
		}
		selected[p.String()] = true
	}
	direct := map[string]bool{}
	for k, v := range selected {
		direct[k] = v
	}
	if request.Cascade {
		identities := []packageinfo.Identity{}
		for _, p := range records {
			if selected[p.String()] {
				identities = append(identities, p.Identity)
			}
		}
		removed, err := dependency.Cascade(ctx, candidates(records), identities, dependency.DefaultLimits())
		if err != nil {
			return result, failure("DEPENDENCY_ERROR", err)
		}
		for _, id := range removed {
			selected[id.String()] = true
		}
	}
	remaining := []packagedatabase.Record{}
	remove := []packagedatabase.Record{}
	for _, p := range records {
		if selected[p.String()] {
			remove = append(remove, p)
			action := "remove"
			if !direct[p.String()] {
				action = "cascade"
			}
			result.Actions = append(result.Actions, Action{Package: p.Identity, Action: action})
		} else {
			remaining = append(remaining, p)
		}
	}
	if _, err := dependency.ValidateAll(ctx, candidates(remaining), dependency.DefaultLimits()); err != nil {
		return result, failure("DEPENDENCY_ERROR", err)
	}
	resolved()
	if len(result.Ignored) > 0 {
		emit(observer, "ignored", result.Ignored)
	}
	emit(observer, "plan", result.Actions)
	if !request.DryRun {
		_, committed := beginProgress(observer, "commit", m.Root)
		if err = s.Commit(ctx, remove, nil); err != nil {
			return result, err
		}
		committed()
	}
	result.Diagnostics = s.Diagnostics
	for _, a := range result.Actions {
		emit(observer, "result", a)
	}
	emit(observer, "summary", result)
	return result, nil
}
