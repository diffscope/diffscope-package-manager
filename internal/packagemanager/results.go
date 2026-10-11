package packagemanager

import (
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

// InstalledPackage is an application result, independent of database row types.
type InstalledPackage struct {
	packageinfo.Identity
	CompatVersion packageinfo.Version      `json:"compatVersion"`
	Hash          string                   `json:"hash"`
	InstalledAt   int64                    `json:"installedAt"`
	Directory     string                   `json:"directory"`
	Dependencies  []packageinfo.Dependency `json:"dependencies"`
	Name          packageinfo.Text         `json:"name,omitempty"`
	Description   packageinfo.Text         `json:"description,omitempty"`
	Vendor        packageinfo.Text         `json:"vendor,omitempty"`
	Contributions []ContributionSummary    `json:"contributions"`
}

type ContributionSummary struct {
	Category  string           `json:"category"`
	ID        string           `json:"id"`
	Interface string           `json:"interface,omitempty"`
	Level     string           `json:"level,omitempty"`
	Variant   string           `json:"variant,omitempty"`
	Name      packageinfo.Text `json:"name,omitempty"`
}
