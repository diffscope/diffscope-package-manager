package packagemanager

import (
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/localstore"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"path/filepath"
)

type Info struct {
	Package      packageinfo.PackageManifest `json:"package"`
	Installation *InstalledPackage           `json:"installation,omitempty"`
	File         string                      `json:"file,omitempty"`
	Hash         string                      `json:"hash,omitempty"`
}

func (m *Manager) InfoFile(ctx context.Context, filename string) (Info, []string, error) {
	var result Info
	a, err := packagearchive.Open(filename, m.Limits)
	if err != nil {
		return result, nil, failure("INVALID_PACKAGE", err)
	}
	defer a.Close()
	root, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return result, nil, err
	}
	result.Package, err = packageinfo.Load(a.Files(), root)
	if err != nil {
		return result, nil, failure("INVALID_PACKAGE", err)
	}
	result.Hash, err = a.Hash(ctx)
	result.File = filename
	return result, result.Package.Diagnostics, err
}
func (m *Manager) InfoInstalled(ctx context.Context, target string) (Info, []string, error) {
	var result Info
	if err := ctx.Err(); err != nil {
		return result, nil, err
	}
	selector, err := packageinfo.ParseSelector(target)
	if err != nil {
		return result, nil, failure("INVALID_ARGUMENT", err)
	}
	s, err := localstore.Open(m.Root, false)
	if err != nil {
		return result, nil, err
	}
	defer s.Close()
	records, err := s.Records()
	if err != nil {
		return result, nil, err
	}
	record, err := resolve(records, selector)
	if err != nil {
		return result, s.Diagnostics, err
	}
	result.Package, err = s.Load(record)
	if err != nil {
		return result, s.Diagnostics, err
	}
	installation := installedResult(record)
	result.Installation = &installation
	if selector.Category != "" {
		found := false
		for _, c := range result.Package.Contributions {
			if c.Category == selector.Category && c.ID == selector.ContributionID {
				result.Package.Contributions = []packageinfo.Contribution{c}
				found = true
				break
			}
		}
		if !found {
			return result, s.Diagnostics, failure("NOT_FOUND", fmt.Errorf("The requested contribution is not installed."))
		}
	}
	return result, append(s.Diagnostics, result.Package.Diagnostics...), nil
}
func (m *Manager) List(ctx context.Context) ([]InstalledPackage, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s, err := localstore.Open(m.Root, false)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	records, err := s.Records()
	result := make([]InstalledPackage, 0, len(records))
	for _, r := range records {
		result = append(result, installedResult(r))
	}
	return result, s.Diagnostics, err
}

func installedResult(r packagedatabase.Record) InstalledPackage {
	result := InstalledPackage{Identity: r.Identity, CompatVersion: r.CompatVersion, Hash: r.Hash, InstalledAt: r.InstalledAt, Directory: r.Directory, Dependencies: r.Dependencies, Name: r.Name, Description: r.Description, Vendor: r.Vendor, Contributions: []ContributionSummary{}}
	for _, c := range r.Contributions {
		result.Contributions = append(result.Contributions, ContributionSummary{Category: c.Category, ID: c.ID, Interface: c.Interface, Level: c.Level, Variant: c.Variant, Name: c.Name})
	}
	return result
}
