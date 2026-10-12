package packagemanager

import (
	"context"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/diskspace"
	"github.com/diffscope/diffscope-package-manager/internal/download"
	"github.com/diffscope/diffscope-package-manager/internal/downloadcache"

	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"github.com/diffscope/diffscope-package-manager/internal/registry"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	dep "github.com/diffscope/diffscope-package-manager/packagedependency"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type RemoteRequest struct {
	Targets      []string
	File         bool
	Overwrite    bool
	DryRun       bool
	Settings     config.Remote
	Unrestricted bool
}
type remoteSession struct {
	settings     config.Remote
	policy       netpolicy.Policy
	cache        *downloadcache.Cache
	sources      []*registry.Session
	packagesRoot string
	budget       registry.Budget
}

func (s *remoteSession) resetSources() {
	for _, r := range s.sources {
		r.Close()
	}
	s.sources = nil
}
func (s *remoteSession) close() {
	s.resetSources()
	if s.cache != nil {
		s.cache.Close()
		s.cache = nil
	}
}
func (s *remoteSession) initialize(ctx context.Context) error {
	if s.cache != nil && s.sources != nil {
		return nil
	}
	if s.cache == nil {
		c, err := downloadcache.Open(ctx, s.settings.CacheDir, s.packagesRoot, true)
		if err != nil {
			return err
		}
		s.cache = c
		if err = c.Lease(ctx); err != nil {
			return err
		}
	}
	s.sources = []*registry.Session{}
	for i, r := range s.settings.Registries {
		source, err := registry.New(r.URL, r.Name, i, s.policy, s.cache, &s.budget)
		if err != nil {
			return err
		}
		s.sources = append(s.sources, source)
	}
	return nil
}
func (s *remoteSession) resolve(ctx context.Context, id string, exact, target *packageinfo.Version) (registry.Resolved, error) {
	if err := s.initialize(ctx); err != nil {
		return registry.Resolved{}, err
	}
	for _, source := range s.sources {
		if !registry.Allowed(id, source.Source, s.settings.SourceRules) {
			continue
		}
		m, ok, err := source.Resolve(ctx, id, exact, target)
		if err != nil {
			return m, err
		}
		if ok {
			return m, nil
		}
	}
	return registry.Resolved{}, failure("NOT_FOUND", fmt.Errorf("No matching package %s is available in the configured sources.", id))
}
func remoteFailure(err error) error {
	if err == nil {
		return nil
	}
	if diskspace.IsFull(err) {
		return failure("DISK_FULL", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return failure("DOWNLOAD_TIMEOUT", err)
	}
	var managerErr *Error
	if errors.As(err, &managerErr) {
		return err
	}
	var transfer *download.Error
	if errors.As(err, &transfer) {
		details := []Diagnostic{}
		if out, ok := transfer.Details.(download.Outcome); ok {
			details = downloadDiagnostics([]download.Outcome{out})
		}
		return &Error{Code: transfer.Code, Message: transfer.Message, Details: transfer.Details, DiagnosticDetails: details, Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	code := "DOWNLOAD_FAILED"
	for _, c := range []string{"REPOSITORY_INCONSISTENT", "INVALID_REMOTE_MANIFEST", "UNSUPPORTED_REPOSITORY_FORMAT", "DOWNLOAD_POLICY_VIOLATION", "INVALID_CACHE_DIRECTORY", "CACHE_BUSY", "NONPORTABLE_METADATA", "INPUT_CHANGED", "DOWNLOAD_TIMEOUT", "RATE_LIMITED", "DISK_FULL"} {
		if strings.Contains(err.Error(), c) {
			code = c
			break
		}
	}
	return failure(code, err)
}

type RemoteInfo struct {
	Source                string                      `json:"source"`
	Package               repo.RemoteManifest         `json:"package"`
	Repository            registry.Source             `json:"repository"`
	ManifestSha512        string                      `json:"manifestSha512"`
	AssetURLs             map[string]packageinfo.Text `json:"assetUrls"`
	NetworkPolicy         string                      `json:"networkPolicy"`
	RepositoryConformance string                      `json:"repositoryConformance"`
	AddressValidation     string                      `json:"addressValidation"`
	DiagnosticDetails     []Diagnostic                `json:"-"`
}

func InfoRemote(ctx context.Context, target string, settings config.Remote, unrestricted bool) (RemoteInfo, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result RemoteInfo
	selector, err := packageinfo.ParseSelector(target)
	if err != nil {
		return result, nil, failure("INVALID_ARGUMENT", err)
	}
	if selector.Category != "" && selector.Category != "singer" {
		return result, nil, failure("UNSUPPORTED_REMOTE_CONTRIBUTION", fmt.Errorf("Remote contribution queries support only singer summaries."))
	}
	s := &remoteSession{settings: settings, policy: netpolicy.Policy{Unrestricted: unrestricted, AllowedHosts: settings.AllowedDownloadHosts}}
	defer s.close()
	var found registry.Resolved
	for round := 0; round < 3; round++ {
		found, err = s.resolve(ctx, selector.ID, selector.Version, nil)
		if !errors.Is(err, registry.ErrInconsistent) {
			break
		}
		s.resetSources()
	}
	if err != nil {
		return result, nil, remoteFailure(err)
	}
	result = RemoteInfo{Source: "remote", Package: found.Manifest, Repository: found.Source, ManifestSha512: found.ManifestSha512, AssetURLs: map[string]packageinfo.Text{}, NetworkPolicy: s.policy.Name(), RepositoryConformance: s.policy.Conformance()}
	result.AddressValidation = s.policy.AddressValidation(found.Source.URL)
	result.DiagnosticDetails = []Diagnostic{}
	for _, v := range []struct {
		field string
		text  packageinfo.Text
	}{{"license", found.Manifest.License}, {"readme", found.Manifest.Readme}} {
		if v.text == nil {
			continue
		}
		result.AssetURLs[v.field] = packageinfo.Text{}
		for lang, p := range v.text {
			result.AssetURLs[v.field][lang] = registry.Join(found.Source.URL, found.Manifest.ID+"/@assets/"+p)
		}
	}
	if selector.Category != "" {
		result.Package.Singers = nil
		for _, singer := range found.Manifest.Singers {
			if singer.ID == selector.ContributionID {
				result.Package.Singers = []repo.SingerSummary{singer}
				break
			}
		}
		if len(result.Package.Singers) == 0 {
			return result, nil, failure("NOT_FOUND", fmt.Errorf("The requested singer is absent."))
		}
	}
	diagnostics := []string{}
	if found.Manifest.Deprecated {
		diagnostics = append(diagnostics, "PACKAGE_DEPRECATED: "+found.Manifest.String()+": "+found.Manifest.DeprecationReason)
		result.DiagnosticDetails = append(result.DiagnosticDetails, deprecatedDiagnostic(found))
	}
	return result, diagnostics, nil
}
func deprecatedDiagnostic(r registry.Resolved) Diagnostic {
	i := r.Manifest.Identity
	return Diagnostic{Code: "PACKAGE_DEPRECATED", Severity: "warning", Identity: &i, SourceID: r.Source.ID, Message: r.Manifest.DeprecationReason}
}
func downloadDiagnostics(outcomes []download.Outcome) []Diagnostic {
	details := []Diagnostic{}
	for _, out := range outcomes {
		for _, rejected := range out.RejectedCandidates {
			i := out.Identity
			details = append(details, Diagnostic{Code: "DOWNLOAD_SOURCE_REJECTED", Severity: "warning", Identity: &i, SourceID: rejected.SourceID, Message: rejected.Reason})
		}
	}
	return details
}

type preparedPackage struct {
	packageinfo.Identity
	CompatVersion     packageinfo.Version      `json:"compatVersion"`
	Dependencies      []packageinfo.Dependency `json:"dependencies"`
	SourceKind        string                   `json:"sourceKind"`
	AuthoritySourceID string                   `json:"authoritySourceId,omitempty"`
	ManifestSha512    string                   `json:"manifestSha512,omitempty"`
	DownloadSha512    string                   `json:"downloadSha512,omitempty"`
	ContentTreeSha512 string                   `json:"contentTreeSha512,omitempty"`
}

func candidateOf(p packageinfo.PackageManifest) dep.Candidate {
	return dep.Candidate{Identity: p.Identity, CompatVersion: p.CompatVersion, Dependencies: p.Dependencies}
}
func remoteCandidate(p repo.RemoteManifest) dep.Candidate {
	return dep.Candidate{Identity: p.Identity, CompatVersion: p.CompatVersion, Dependencies: p.Dependencies}
}

type acquisition struct {
	Requester  packageinfo.Identity   `json:"requester"`
	Required   packageinfo.Dependency `json:"required"`
	Provider   packageinfo.Identity   `json:"provider"`
	SourceKind string                 `json:"sourceKind"`
	SourceID   string                 `json:"sourceId,omitempty"`
}
type ResolutionPlan struct {
	OperationID                  string                 `json:"operationId"`
	InstalledSnapshotFingerprint string                 `json:"installedSnapshotFingerprint"`
	VerificationScope            string                 `json:"verificationScope"`
	Sources                      []registry.Source      `json:"sources"`
	SourceOrder                  []string               `json:"sourceOrder"`
	Roots                        []packageinfo.Identity `json:"roots"`
	Packages                     []preparedPackage      `json:"packages"`
	AcquisitionDecisions         []acquisition          `json:"acquisitionDecisions"`
	EffectiveEdges               []dep.Edge             `json:"effectiveEdges"`
	PendingArchiveChecks         []packageinfo.Identity `json:"pendingArchiveChecks"`
	NetworkPolicy                string                 `json:"networkPolicy"`
	RepositoryConformance        string                 `json:"repositoryConformance"`
}

func (m *Manager) InstallRemote(ctx context.Context, request RemoteRequest, observer Observer) (MutationResult, error) {
	result := MutationResult{Actions: []Action{}, DryRun: request.DryRun}
	if len(request.Targets) == 0 {
		return result, failure("INVALID_ARGUMENT", fmt.Errorf("At least one installation target is required."))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	operationID := netpolicy.Key(fmt.Sprint(time.Now().UnixNano()) + m.Root)
	var observerMu sync.Mutex
	originalObserver := observer
	observer = func(e Event) {
		observerMu.Lock()
		defer observerMu.Unlock()
		if originalObserver != nil {
			originalObserver(e)
		}
	}
	s := &remoteSession{settings: request.Settings, policy: netpolicy.Policy{Unrestricted: request.Unrestricted, AllowedHosts: request.Settings.AllowedDownloadHosts}, packagesRoot: m.Root}
	defer s.close()
	var engine *download.Engine
	defer func() {
		if engine != nil {
			engine.Close()
		}
	}()
	type acquired struct {
		filename string
		outcome  download.Outcome
		manifest repo.RemoteManifest
		sourceID string
	}
	completed := map[string]acquired{}
	var completedMu sync.Mutex
	inputs := []installInput{}
	defer func() {
		for _, v := range inputs {
			v.archive.Close()
		}
	}()
	locals := []dep.Candidate{}
	localMap := map[string]bool{}
	if request.File {
		root, err := filepath.Abs(m.Root)
		if err != nil {
			return result, err
		}
		for _, filename := range request.Targets {
			a, err := packagearchive.Open(filename, m.Limits)
			if err != nil {
				return result, failure("INVALID_PACKAGE", err)
			}
			inputs = append(inputs, installInput{file: filename, archive: a})
			v := &inputs[len(inputs)-1]
			v.manifest, err = packageinfo.Load(a.Files(), root)
			if err != nil {
				return result, failure("INVALID_PACKAGE", err)
			}
			if localMap[v.manifest.String()] {
				return result, failure("CONFLICT", fmt.Errorf("The batch contains a duplicate package identity %s.", v.manifest.String()))
			}
			localMap[v.manifest.String()] = true
			v.hash, err = a.Hash(ctx)
			if err != nil {
				return result, failure("INVALID_PACKAGE", err)
			}
			locals = append(locals, candidateOf(v.manifest))
		}
	}
	// A bounded retry repeats planning after a concurrent store change. Network
	// activity happens only after the short snapshot session has been closed.
	for storeRound := 0; storeRound < 3; storeRound++ {
		store, err := openStore(ctx, m.Root, false, observer)
		if err != nil {
			return result, err
		}
		existing, err := store.Records()
		diagnostics := append([]string{}, store.Diagnostics...)
		store.Close()
		if err != nil {
			return result, err
		}
		installed := candidates(existing)
		for _, input := range inputs {
			for _, old := range existing {
				if old.String() == input.manifest.String() && old.Hash != input.hash && !request.Overwrite {
					return result, failure("CONFLICT", fmt.Errorf("Different contents are already installed as %s.", old.String()))
				}
			}
		}
		var selected map[string]registry.Resolved
		var plan ResolutionPlan
		planningCtx, planningCancel := context.WithTimeout(ctx, 2*time.Minute)
		build := func() error {
			selected = map[string]registry.Resolved{}
			processed := map[string]bool{}
			current := map[string]dep.Candidate{}
			for _, c := range installed {
				current[c.String()] = c
			}
			roots := []packageinfo.Identity{}
			for _, c := range locals {
				current[c.String()] = c
				roots = append(roots, c.Identity)
			}
			add := func(r registry.Resolved) error {
				if old, ok := selected[r.Manifest.String()]; ok && !repo.SameContent(old.Manifest, r.Manifest) {
					return failure("SOURCE_IDENTITY_CONFLICT", fmt.Errorf("The sources specify different contents for %s.", r.Manifest.String()))
				}
				if old, ok := current[r.Manifest.String()]; ok && len(repo.Differences(old, remoteCandidate(r.Manifest))) > 0 {
					for k := range processed {
						if strings.HasPrefix(k, r.Manifest.String()+":") {
							delete(processed, k)
						}
					}
				}
				selected[r.Manifest.String()] = r
				current[r.Manifest.String()] = remoteCandidate(r.Manifest)
				return nil
			}
			if !request.File {
				seen := map[string]bool{}
				resolvedRoots := map[string]bool{}
				for _, raw := range request.Targets {
					t, err := packageinfo.ParseSelector(raw)
					if err != nil || t.Category != "" {
						return failure("INVALID_ARGUMENT", fmt.Errorf("The installation selector %s is invalid.", raw))
					}
					key := t.ID
					if t.Version != nil {
						key += "=" + t.Version.String()
					}
					if seen[key] {
						return failure("CONFLICT", fmt.Errorf("The installation roots contain a duplicate selector %s.", key))
					}
					seen[key] = true
					r, err := s.resolve(planningCtx, t.ID, t.Version, nil)
					if err != nil {
						return err
					}
					if resolvedRoots[r.Manifest.String()] {
						return failure("CONFLICT", fmt.Errorf("The resolved roots contain a duplicate identity %s.", r.Manifest.String()))
					}
					resolvedRoots[r.Manifest.String()] = true
					if err = add(r); err != nil {
						return err
					}
					roots = append(roots, r.Manifest.Identity)
				}
			}
			decisions := []acquisition{}
			// Refuse an unrelated pre-existing missing dependency. A batch can
			// replace its requester or explicitly provide the missing package,
			// but an ordinary install must not initiate a whole-store repair.
			initialUnion := []dep.Candidate{}
			for _, c := range current {
				initialUnion = append(initialUnion, c)
			}
			for _, old := range installed {
				if _, replaced := selected[old.String()]; replaced || localMap[old.String()] {
					continue
				}
				for _, required := range old.Dependencies {
					if _, hadProvider := dep.Select(installed, required); hadProvider {
						continue
					}
					if _, repaired := dep.Select(initialUnion, required); !repaired {
						return failure("DEPENDENCY_ERROR", fmt.Errorf("The existing store is missing %s=%s required by %s. Explicit repair is required.", required.ID, required.Version.String(), old.String()))
					}
				}
			}

			for {
				if len(current) > 10000 {
					return fmt.Errorf("The candidate count exceeds its budget.")
				}
				keys := []string{}
				for key := range current {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				changed := false
				for _, key := range keys {
					c := current[key]
					for _, d := range c.Dependencies {
						decisionKey := key + ":" + d.ID
						if processed[decisionKey] {
							continue
						}
						processed[decisionKey] = true
						provider, found := dep.Select(locals, d)
						kind := "local"
						sourceID := ""
						if !found {
							filtered := []dep.Candidate{}
							for _, v := range installed {
								if _, replaced := selected[v.String()]; !localMap[v.String()] && !replaced {
									filtered = append(filtered, v)
								}
							}
							provider, found = dep.Select(filtered, d)
							kind = "installed"
						}
						if !found {
							r, err := s.resolve(planningCtx, d.ID, nil, &d.Version)
							if err != nil {
								return failure("DEPENDENCY_ERROR", fmt.Errorf("%s requires %s=%s: %w", key, d.ID, d.Version.String(), err))
							}
							_, already := selected[r.Manifest.String()]
							if err = add(r); err != nil {
								return err
							}
							changed = changed || !already
							provider = remoteCandidate(r.Manifest)
							sourceID = r.Source.ID
							kind = "remote"
						}
						decisions = append(decisions, acquisition{c.Identity, d, provider.Identity, kind, sourceID})
					}
				}
				if !changed {
					break
				}
			}
			all := []dep.Candidate{}
			for _, c := range current {
				all = append(all, c)
			}
			graph, err := dep.ValidateAll(ctx, all, dep.DefaultLimits())
			if err != nil {
				return failure("DEPENDENCY_ERROR", err)
			}
			plan = ResolutionPlan{OperationID: operationID, InstalledSnapshotFingerprint: snapshotFingerprint(existing), VerificationScope: "metadata-only", Sources: []registry.Source{}, SourceOrder: []string{}, Roots: roots, Packages: []preparedPackage{}, AcquisitionDecisions: decisions, EffectiveEdges: graph.Edges, PendingArchiveChecks: []packageinfo.Identity{}, NetworkPolicy: s.policy.Name(), RepositoryConformance: s.policy.Conformance()}
			for _, candidate := range graph.Nodes {
				item := preparedPackage{Identity: candidate.Identity, CompatVersion: candidate.CompatVersion, Dependencies: candidate.Dependencies, SourceKind: "installed"}
				if r, ok := selected[candidate.String()]; ok {
					item.SourceKind = "remote"
					item.AuthoritySourceID = r.Source.ID
					item.ManifestSha512 = r.ManifestSha512
					item.DownloadSha512 = r.Manifest.DownloadSha512
				} else if localMap[candidate.String()] {
					item.SourceKind = "local"
					for _, input := range inputs {
						if input.manifest.String() == candidate.String() {
							item.ContentTreeSha512 = input.hash
						}
					}
				} else {
					for _, record := range existing {
						if record.String() == candidate.String() {
							item.ContentTreeSha512 = record.Hash
						}
					}
				}
				plan.Packages = append(plan.Packages, item)
			}
			for _, source := range request.Settings.Registries {
				plan.SourceOrder = append(plan.SourceOrder, source.URL)
			}
			for _, source := range s.sources {
				if source.Source.Revision != "" {
					plan.Sources = append(plan.Sources, source.Source)
				}
			}
			for _, r := range selected {
				plan.PendingArchiveChecks = append(plan.PendingArchiveChecks, r.Manifest.Identity)
			}
			sort.Slice(plan.PendingArchiveChecks, func(i, j int) bool {
				return plan.PendingArchiveChecks[i].String() < plan.PendingArchiveChecks[j].String()
			})
			return nil
		}
		_, metadataDone := beginProgress(observer, "metadata", "remote dependency plan")
		for round := 0; round < 3; round++ {
			err = build()
			if !errors.Is(err, registry.ErrInconsistent) {
				break
			}
			s.resetSources()
		}
		planningCancel()
		if err != nil {
			return result, remoteFailure(err)
		}
		metadataDone()
		if len(selected) == 0 {
			files := []string{}
			for _, v := range inputs {
				files = append(files, v.file)
			}
			localResult, localErr := m.Install(ctx, InstallRequest{Files: files, Overwrite: request.Overwrite, DryRun: request.DryRun, prepared: inputs, expectedSnapshot: snapshotFingerprint(existing)}, observer)
			var changed *Error
			if errors.As(localErr, &changed) && changed.Code == "STORE_CHANGED" && storeRound < 2 {
				continue
			}
			return localResult, localErr
		}
		diagnosticDetails := []Diagnostic{}
		for _, r := range selected {
			if r.Manifest.Deprecated {
				diagnosticDetails = append(diagnosticDetails, deprecatedDiagnostic(r))
			}
		}
		sort.Slice(diagnosticDetails, func(i, j int) bool {
			return diagnosticDetails[i].Identity.String() < diagnosticDetails[j].Identity.String()
		})
		emit(observer, "resolution_plan", plan)
		if request.DryRun {
			emit(observer, "summary", map[string]any{"actions": []Action{}, "dryRun": true, "verification": "metadata-only", "plannedActions": plan.Packages, "pendingArchiveChecks": plan.PendingArchiveChecks, "commitOutcome": "not-started", "diagnosticDetails": diagnosticDetails, "sources": plan.Sources, "networkPolicy": s.policy.Name(), "repositoryConformance": s.policy.Conformance()})
			return result, nil
		}
		if engine == nil {
			engine = download.New(s.cache, s.policy, func(p download.Progress) { emit(observer, "progress", p) })
			engine.OperationID = operationID
			engine.MaxBytes = request.Settings.MaxDownloadBytes
		}
		keys := []string{}
		for key := range selected {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		paths := make([]string, len(keys))
		outcomes := make([]download.Outcome, len(keys))
		errs := make([]error, len(keys))
		semaphore := make(chan struct{}, request.Settings.DownloadConcurrency)
		var wg sync.WaitGroup
		for i, key := range keys {
			wg.Add(1)
			go func(i int, key string) {
				defer wg.Done()
				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()
				case <-ctx.Done():
					errs[i] = ctx.Err()
					return
				}
				authority := selected[key]
				completedMu.Lock()
				previous, reused := completed[key]
				completedMu.Unlock()
				if reused {
					if !repo.SameContent(previous.manifest, authority.Manifest) || previous.sourceID != authority.Source.ID {
						errs[i] = failure("SOURCE_IDENTITY_CONFLICT", fmt.Errorf("The authority source changed for prepared identity %s.", key))
						cancel()
						return
					}
					paths[i], outcomes[i] = previous.filename, previous.outcome
					return
				}
				next := 0
				discover := func(ctx context.Context) (*registry.Resolved, error) {
					for next < len(s.sources) {
						source := s.sources[next]
						next++
						if source.Source.ID == authority.Source.ID || !registry.Allowed(authority.Manifest.ID, source.Source, request.Settings.SourceRules) {
							continue
						}
						candidate, ok, err := source.Resolve(ctx, authority.Manifest.ID, &authority.Manifest.Version, nil)
						if err != nil {
							return nil, &download.Error{Code: "DOWNLOAD_SOURCE_REJECTED", Message: err.Error(), Details: map[string]any{"sourceId": source.Source.ID}}
						}
						if !ok {
							return nil, &download.Error{Code: "DOWNLOAD_SOURCE_REJECTED", Message: "The exact package identity is absent from the optional source.", Details: map[string]any{"sourceId": source.Source.ID}}
						}
						return &candidate, nil
					}
					return nil, nil
				}
				paths[i], outcomes[i], errs[i] = engine.Acquire(ctx, authority, discover)
				if errs[i] == nil {
					completedMu.Lock()
					completed[key] = acquired{paths[i], outcomes[i], authority.Manifest, authority.Source.ID}
					completedMu.Unlock()
				}
				if errs[i] != nil {
					cancel()
				}
			}(i, key)
		}
		wg.Wait()
		var acquisitionErr error
		for _, e := range errs {
			if e != nil && !errors.Is(e, context.Canceled) {
				acquisitionErr = e
				break
			}
		}
		if acquisitionErr == nil {
			for _, e := range errs {
				if e != nil {
					acquisitionErr = e
					break
				}
			}
		}
		if acquisitionErr != nil {
			return result, remoteFailure(acquisitionErr)
		}
		prepared := append([]installInput{}, inputs...)
		held := []*os.File{}
		closePrepared := func() {
			for _, f := range held {
				f.Close()
			}
		}
		for i, filename := range paths {
			r := selected[keys[i]]
			f, err := os.Open(filename)
			if err != nil {
				closePrepared()
				return result, err
			}
			held = append(held, f)
			st, err := f.Stat()
			if err != nil {
				closePrepared()
				return result, err
			}
			fileDigest, err := packagearchive.HashFileBytes(ctx, f, st.Size(), nil)
			if err != nil || fileDigest != r.Manifest.DownloadSha512 {
				closePrepared()
				return result, failure("INPUT_CHANGED", fmt.Errorf("The cached archive changed before preparation: %v", err))
			}
			a, err := packagearchive.NewReaderAt(f, st.Size(), m.Limits)
			if err != nil {
				closePrepared()
				return result, failure("INVALID_PACKAGE", err)
			}
			progress, done := beginProgress(observer, "archive-verify", r.Manifest.String())
			tree, err := a.HashWithProgress(ctx, progress)
			if err != nil {
				closePrepared()
				return result, failure("INVALID_PACKAGE", err)
			}
			projection, err := repo.ProjectFS(ctx, a.Files(), repo.ProjectionOptions{Limits: repo.ProjectionLimits{MaxAssetBytes: 32 << 20}})
			if err != nil {
				closePrepared()
				return result, failure("INVALID_PACKAGE", err)
			}
			diff, err := repo.CompareMetadata(r.Manifest, projection)
			if err != nil || len(diff) > 0 {
				closePrepared()
				return result, &Error{Code: "PACKAGE_METADATA_MISMATCH", Message: "The archive metadata differs from the repository manifest.", Details: diff, Cause: err}
			}
			root, _ := filepath.Abs(m.Root)
			manifest, err := packageinfo.Load(a.Files(), root)
			if err != nil {
				closePrepared()
				return result, failure("INVALID_PACKAGE", err)
			}
			prepared = append(prepared, installInput{filename, a, manifest, tree})
			done()
			if r.Manifest.Deprecated {
				diagnostics = append(diagnostics, "PACKAGE_DEPRECATED: "+r.Manifest.String()+": "+r.Manifest.DeprecationReason)
			}
		}
		files := []string{}
		for _, v := range prepared {
			files = append(files, v.file)
		}
		// Install reopens the store exclusively and recomputes identity overlays and
		// the complete graph before staging. Prepared handles avoid a second ZIP scan.
		result, err = m.Install(ctx, InstallRequest{Files: files, Overwrite: request.Overwrite, prepared: prepared, expectedSnapshot: snapshotFingerprint(existing)}, func(e Event) {
			if e.Event == "summary" {
				return
			}
			emit(observer, e.Event, e.Data)
		})
		closePrepared()
		if err != nil {
			var me *Error
			if errors.As(err, &me) && me.Code == "STORE_CHANGED" && storeRound < 2 {
				emit(observer, "plan_updated", map[string]any{"reason": "Installed dependencies changed during preparation.", "rebuild": storeRound + 1})
				continue
			}
			return result, err
		}
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		if result.Diagnostics == nil {
			result.Diagnostics = []string{}
		}
		outcome := "committed"
		allSkip := true
		for _, a := range result.Actions {
			if a.Action != "skip" {
				allSkip = false
			}
		}
		if allSkip {
			outcome = "no-change"
		}
		// Include work completed before a store replan, even when a later plan
		// no longer needs that archive. A transport contributes exactly once.
		completedKeys := []string{}
		for key := range completed {
			completedKeys = append(completedKeys, key)
		}
		sort.Strings(completedKeys)
		allOutcomes := []download.Outcome{}
		for _, key := range completedKeys {
			allOutcomes = append(allOutcomes, completed[key].outcome)
		}
		emit(observer, "summary", map[string]any{"actions": result.Actions, "dryRun": false, "sources": plan.Sources, "downloadStats": allOutcomes, "diagnosticDetails": append(diagnosticDetails, downloadDiagnostics(allOutcomes)...), "diagnostics": result.Diagnostics, "commitOutcome": outcome, "networkPolicy": s.policy.Name(), "repositoryConformance": s.policy.Conformance()})
		return result, nil
	}
	return result, failure("STORE_CHANGED", fmt.Errorf("The installed store changed repeatedly during preparation."))
}
