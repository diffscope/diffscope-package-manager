package packagemanager

import (
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"os"
)

func (m *Manager) Pack(ctx context.Context, source, output string, force, dryRun bool, observer Observer) (packagearchive.PackPlan, error) {
	_, checked := beginProgress(observer, "check", source)
	if info, err := os.Lstat(output); err == nil {
		if info.IsDir() || !force {
			return packagearchive.PackPlan{}, failure("CONFLICT", fmt.Errorf("output already exists; --force is required to replace a file"))
		}
	} else if !os.IsNotExist(err) {
		return packagearchive.PackPlan{}, err
	}
	plan, err := packagearchive.Plan(ctx, source, output, m.Limits)
	if err != nil {
		return plan, failure("INVALID_PACKAGE", err)
	}
	checked()
	emit(observer, "plan", plan)
	if dryRun {
		emit(observer, "summary", map[string]any{"dryRun": true, "diagnostics": plan.Package.Diagnostics})
		return plan, nil
	}
	progress, packed := beginProgress(observer, "pack", plan.Package.String())
	var verify packagearchive.ProgressFunc
	var verified func()
	hash, err := packagearchive.PackWithProgress(ctx, plan, force, m.Limits, progress, func(p packagearchive.Progress) {
		if verify == nil {
			packed()
			verify, verified = beginProgress(observer, "verify", plan.Package.String())
		}
		verify(p)
	})
	if err != nil {
		return plan, err
	}
	verified()
	info, err := os.Stat(plan.Output)
	if err != nil {
		return plan, err
	}
	emit(observer, "result", map[string]any{"file": plan.Output, "hash": hash, "package": plan.Package.Identity, "size": info.Size()})
	emit(observer, "summary", map[string]any{"diagnostics": plan.Package.Diagnostics})
	return plan, nil
}
