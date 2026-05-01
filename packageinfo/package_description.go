package packageinfo

// PackageDescription describes the package-desc.schema.json document.
type PackageDescription struct {
	Contributes  PackageContributions `json:"contributes" validate:"required"`
	Dependencies []PackageDependency  `json:"dependencies" validate:"required,dive"`
	Description  *MultilingualText    `json:"description,omitempty"`
	ID           string               `json:"id" validate:"required,dspm_package_id"`
	License      *MultilingualText    `json:"license,omitempty"`
	Name         *MultilingualText    `json:"name,omitempty"`
	Readme       *MultilingualText    `json:"readme,omitempty"`
	URL          string               `json:"url,omitempty"`
	Vendor       *MultilingualText    `json:"vendor,omitempty"`
	Version      PackageVersion       `json:"version" validate:"required,dspm_package_version"`
}

// PackageContributions lists package-local resource description files.
type PackageContributions struct {
	Inferences []string `json:"inferences" validate:"required,dive,required"`
	Singers    []string `json:"singers" validate:"required,dive,required"`
}

// PackageDependency describes a dependency on another package.
type PackageDependency struct {
	ID      string         `json:"id" validate:"required,dspm_package_id"`
	Version PackageVersion `json:"version" validate:"required,dspm_package_version"`
}
