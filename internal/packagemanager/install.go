package packagemanager

import (
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/diskspace"
	"github.com/diffscope/diffscope-package-manager/internal/localstore"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	dependency "github.com/diffscope/diffscope-package-manager/packagedependency"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"path/filepath"
	"time"
)

type InstallRequest struct {
	Files            []string
	Overwrite        bool
	DryRun           bool
	prepared         []installInput
	expectedSnapshot string
}
type installInput struct {
	file     string
	archive  *packagearchive.Archive
	manifest packageinfo.PackageManifest
	hash     string
}

func (m *Manager) Install(ctx context.Context, request InstallRequest, observer Observer) (MutationResult, error) {
	result := MutationResult{Actions: []Action{}, DryRun: request.DryRun}
	if len(request.Files) == 0 {
		return result, failure("INVALID_ARGUMENT", fmt.Errorf("At least one package file is required."))
	}
	inputs := append([]installInput{}, request.prepared...)
	defer func() {
		if request.prepared != nil {
			return
		}
		for _, input := range inputs {
			input.archive.Close()
		}
	}()
	identities := map[string]bool{}
	emit(observer, "inspect_start", request.Files)
	virtualRoot, err := filepath.Abs(m.Root)
	if err != nil {
		return result, err
	}
	inspectFiles := request.Files
	if request.prepared != nil {
		inspectFiles = nil
	}
	for _, filename := range inspectFiles {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		progress, inspected := beginProgress(observer, "inspect", filename)
		a, err := packagearchive.Open(filename, m.Limits)
		if err != nil {
			return result, failure("INVALID_PACKAGE", err)
		}
		inputs = append(inputs, installInput{file: filename, archive: a})
		input := &inputs[len(inputs)-1]
		input.manifest, err = packageinfo.Load(a.Files(), virtualRoot)
		if err != nil {
			return result, failure("INVALID_PACKAGE", err)
		}
		if identities[input.manifest.String()] {
			return result, failure("CONFLICT", fmt.Errorf("The batch contains a duplicate package identity %s.", input.manifest.String()))
		}
		identities[input.manifest.String()] = true
		input.hash, err = a.HashWithProgress(ctx, progress)
		if err != nil {
			return result, failure("INVALID_PACKAGE", err)
		}
		inspected()
		emit(observer, "inspect_ok", input.manifest.Identity)
	}
	_, resolved := beginProgress(observer, "resolve", m.Root)
	s, err := openStore(ctx, m.Root, !request.DryRun, observer)
	if err != nil {
		return result, err
	}
	defer s.Close()
	recoveryDiagnostics := len(s.Diagnostics)
	result.Diagnostics = append(result.Diagnostics, s.Diagnostics...)
	existing, err := s.Records()
	if err != nil {
		return result, err
	}
	snapshotChanged := request.expectedSnapshot != "" && request.expectedSnapshot != snapshotFingerprint(existing)
	if snapshotChanged {
		emit(observer, "plan_updated", map[string]any{"reason": "store snapshot changed; actions recomputed"})
	}
	current := map[string]packagedatabase.Record{}
	for _, r := range existing {
		current[r.String()] = r
	}
	remove := []packagedatabase.Record{}
	records := []packagedatabase.Record{}
	for _, input := range inputs {
		p, err := packageinfo.Load(input.archive.Files(), filepath.Join(s.Root, localstore.DirectoryName(input.manifest.Identity)))
		if err != nil {
			return result, failure("INVALID_PACKAGE", err)
		}
		action := "install"
		if old, ok := current[p.String()]; ok {
			if old.Hash == input.hash {
				action = "skip"
			} else if !request.Overwrite {
				return result, failure("CONFLICT", fmt.Errorf("Different content is already installed as %s. Replacement requires --overwrite-existing.", p.String()))
			} else {
				action = "overwrite"
				remove = append(remove, old)
			}
		}
		result.Actions = append(result.Actions, Action{Package: p.Identity, Action: action, File: input.file, Hash: input.hash})
		if action != "skip" {
			r := packagedatabase.Index(p, input.hash, localstore.DirectoryName(p.Identity), time.Now().Unix())
			records = append(records, r)
			current[r.String()] = r
		}
		result.Diagnostics = append(result.Diagnostics, p.Diagnostics...)
	}
	if _, err := dependency.ValidateAll(ctx, candidates(mapRecords(current)), dependency.DefaultLimits()); err != nil {
		if snapshotChanged {
			return result, failure("STORE_CHANGED", err)
		}
		return result, failure("DEPENDENCY_ERROR", err)
	}
	resolved()
	emit(observer, "plan", result.Actions)
	if !request.DryRun {
		var stagingBytes int64
		for i, input := range inputs {
			if result.Actions[i].Action != "skip" {
				stagingBytes += input.archive.UncompressedBytes()
			}
		}
		if stagingBytes > 0 {
			if err := diskspace.Check(s.Root, stagingBytes); err != nil {
				return result, failure("DISK_FULL", err)
			}
		}
		put := []localstore.Put{}
		recordIndex := 0
		for i, input := range inputs {
			if result.Actions[i].Action == "skip" {
				continue
			}
			progress, extracted := beginProgress(observer, "extract", input.manifest.String())
			stage, p, err := s.StageWithProgress(ctx, input.archive, progress)
			if err != nil {
				return result, failure("INVALID_PACKAGE", err)
			}
			// Stage metadata is re-evaluated at the final root, not its temporary location.
			if p.Identity.String() != records[recordIndex].Identity.String() {
				return result, failure("INVALID_PACKAGE", fmt.Errorf("The package identity changed during staging."))
			}
			extracted()
			progress, verified := beginProgress(observer, "verify", p.String())
			hash, err := s.StagedHashWithProgress(ctx, stage, m.Limits, progress)
			if err != nil {
				return result, err
			}
			if hash != input.hash {
				return result, failure("INVALID_PACKAGE", fmt.Errorf("The package bytes changed during staging."))
			}
			finalManifest, err := s.LoadStage(stage, p.Identity)
			if err != nil {
				return result, failure("INVALID_PACKAGE", err)
			}
			record := packagedatabase.Index(finalManifest, hash, localstore.DirectoryName(p.Identity), records[recordIndex].InstalledAt)
			current[record.String()] = record
			put = append(put, localstore.Put{Record: record, Stage: stage})
			recordIndex++
			verified()
			emit(observer, "staged", p.Identity)
		}
		if _, err := dependency.ValidateAll(ctx, candidates(mapRecords(current)), dependency.DefaultLimits()); err != nil {
			if snapshotChanged {
				return result, failure("STORE_CHANGED", err)
			}
			return result, failure("DEPENDENCY_ERROR", err)
		}
		_, committed := beginProgress(observer, "commit", m.Root)
		if err := s.Commit(ctx, remove, put); err != nil {
			return result, err
		}
		committed()
	}
	result.Diagnostics = append(result.Diagnostics, s.Diagnostics[recoveryDiagnostics:]...)
	for _, action := range result.Actions {
		emit(observer, "result", action)
	}
	emit(observer, "summary", result)
	return result, nil
}
