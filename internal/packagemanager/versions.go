package packagemanager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"github.com/diffscope/diffscope-package-manager/internal/registry"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	repo "github.com/diffscope/diffscope-package-manager/packagerepository"
)

// Returns the available versions from the first permitted source containing the package.
// Returns an error for invalid selectors, missing packages, or unavailable metadata.
func VersionsRemote(ctx context.Context, id string, settings config.Remote, unrestricted bool) (repo.VersionsIndex, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	selector, err := packageinfo.ParseSelector(id)
	if err != nil || selector.Version != nil || selector.Category != "" {
		return repo.VersionsIndex{}, failure("INVALID_ARGUMENT", fmt.Errorf("A package ID without a version or contribution is required."))
	}
	s := &remoteSession{settings: settings, policy: netpolicy.Policy{Unrestricted: unrestricted, AllowedHosts: settings.AllowedDownloadHosts}}
	defer s.close()
	for round := 0; round < 3; round++ {
		if err = s.initialize(ctx); err != nil {
			break
		}
		for _, source := range s.sources {
			if !registry.Allowed(id, source.Source, settings.SourceRules) {
				continue
			}
			var index repo.VersionsIndex
			var found bool
			index, found, err = source.Versions(ctx, id)
			if err != nil {
				break
			}
			if found {
				return index, nil
			}
		}
		if !errors.Is(err, registry.ErrInconsistent) {
			break
		}
		s.resetSources()
	}
	if err != nil {
		return repo.VersionsIndex{}, remoteFailure(err)
	}
	return repo.VersionsIndex{}, failure("NOT_FOUND", fmt.Errorf("No matching package %s is available in the configured sources.", id))
}
