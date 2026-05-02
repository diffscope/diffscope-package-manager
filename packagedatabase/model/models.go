package model

// Package stores one installed DiffScope package.
type Package struct {
	ID          string  `gorm:"column:id;type:text;primaryKey;not null"`
	Version     string  `gorm:"column:version;type:text;primaryKey;not null"`
	Hash        string  `gorm:"column:hash;type:text;not null"`
	InstalledAt int64   `gorm:"column:installed_at;not null"`
	URL         *string `gorm:"column:url;type:text"`
}

func (Package) TableName() string {
	return "package"
}

// Dependency stores an installed package dependency edge.
type Dependency struct {
	PackageID               string  `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion          string  `gorm:"column:package_version;type:text;primaryKey;not null"`
	DependentPackageID      string  `gorm:"column:dependent_package_id;type:text;primaryKey;not null"`
	DependentPackageVersion string  `gorm:"column:dependent_package_version;type:text;primaryKey;not null"`
	Package                 Package `gorm:"foreignKey:PackageID,PackageVersion;references:ID,Version;constraint:OnDelete:CASCADE"`
	DependentPackage        Package `gorm:"foreignKey:DependentPackageID,DependentPackageVersion;references:ID,Version;constraint:OnDelete:CASCADE"`
}

func (Dependency) TableName() string {
	return "dependency"
}

// Inference stores an inference module contributed by a package.
type Inference struct {
	ID             string  `gorm:"column:id;type:text;primaryKey;not null"`
	PackageID      string  `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string  `gorm:"column:package_version;type:text;primaryKey;not null"`
	Package        Package `gorm:"foreignKey:PackageID,PackageVersion;references:ID,Version;constraint:OnDelete:CASCADE"`
}

func (Inference) TableName() string {
	return "inference"
}

// Singer stores a singer module contributed by a package.
type Singer struct {
	ID             string  `gorm:"column:id;type:text;primaryKey;not null"`
	PackageID      string  `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string  `gorm:"column:package_version;type:text;primaryKey;not null"`
	Class          string  `gorm:"column:class;type:text;not null"`
	Package        Package `gorm:"foreignKey:PackageID,PackageVersion;references:ID,Version;constraint:OnDelete:CASCADE"`
}

func (Singer) TableName() string {
	return "singer"
}

// SingerImport stores an inference imported by a singer module.
type SingerImport struct {
	SingerID               string    `gorm:"column:singer_id;type:text;primaryKey;not null"`
	PackageID              string    `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion         string    `gorm:"column:package_version;type:text;primaryKey;not null"`
	ImportedInferenceID    string    `gorm:"column:imported_inference_id;type:text;primaryKey;not null"`
	ImportedPackageID      string    `gorm:"column:imported_package_id;type:text;primaryKey;not null"`
	ImportedPackageVersion string    `gorm:"column:imported_package_version;type:text;primaryKey;not null"`
	Singer                 Singer    `gorm:"foreignKey:SingerID,PackageID,PackageVersion;references:ID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
	ImportedInference      Inference `gorm:"foreignKey:ImportedInferenceID,ImportedPackageID,ImportedPackageVersion;references:ID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
}

func (SingerImport) TableName() string {
	return "singer_import"
}

// PackageMultilingualInfo stores localized package display metadata.
type PackageMultilingualInfo struct {
	PackageID      string  `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string  `gorm:"column:package_version;type:text;primaryKey;not null"`
	Language       string  `gorm:"column:language;type:text;primaryKey;not null"`
	Name           *string `gorm:"column:name;type:text"`
	Description    *string `gorm:"column:description;type:text"`
	Vendor         *string `gorm:"column:vendor;type:text"`
	Readme         *string `gorm:"column:readme;type:text"`
	License        *string `gorm:"column:license;type:text"`
	Package        Package `gorm:"foreignKey:PackageID,PackageVersion;references:ID,Version;constraint:OnDelete:CASCADE"`
}

func (PackageMultilingualInfo) TableName() string {
	return "package_multilingual_info"
}

// InferenceMultilingualInfo stores localized inference display metadata.
type InferenceMultilingualInfo struct {
	InferenceID    string    `gorm:"column:inference_id;type:text;primaryKey;not null"`
	PackageID      string    `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string    `gorm:"column:package_version;type:text;primaryKey;not null"`
	Language       string    `gorm:"column:language;type:text;primaryKey;not null"`
	Name           *string   `gorm:"column:name;type:text"`
	Inference      Inference `gorm:"foreignKey:InferenceID,PackageID,PackageVersion;references:ID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
}

func (InferenceMultilingualInfo) TableName() string {
	return "inference_multilingual_info"
}

// SingerMultilingualInfo stores localized singer display metadata.
type SingerMultilingualInfo struct {
	SingerID       string  `gorm:"column:singer_id;type:text;primaryKey;not null"`
	PackageID      string  `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string  `gorm:"column:package_version;type:text;primaryKey;not null"`
	Language       string  `gorm:"column:language;type:text;primaryKey;not null"`
	Name           *string `gorm:"column:name;type:text"`
	Avatar         *string `gorm:"column:avatar;type:text"`
	Background     *string `gorm:"column:background;type:text"`
	Singer         Singer  `gorm:"foreignKey:SingerID,PackageID,PackageVersion;references:ID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
}

func (SingerMultilingualInfo) TableName() string {
	return "singer_multilingual_info"
}

// SingerDemoAudio stores a singer demo audio slot.
type SingerDemoAudio struct {
	Index          int    `gorm:"column:index;primaryKey;not null"`
	SingerID       string `gorm:"column:singer_id;type:text;primaryKey;not null"`
	PackageID      string `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string `gorm:"column:package_version;type:text;primaryKey;not null"`
	Singer         Singer `gorm:"foreignKey:SingerID,PackageID,PackageVersion;references:ID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
}

func (SingerDemoAudio) TableName() string {
	return "singer_demo_audio"
}

// SingerDemoAudioMultilingualInfo stores localized demo audio metadata.
type SingerDemoAudioMultilingualInfo struct {
	DemoIndex      int             `gorm:"column:demo_index;primaryKey;not null"`
	SingerID       string          `gorm:"column:singer_id;type:text;primaryKey;not null"`
	PackageID      string          `gorm:"column:package_id;type:text;primaryKey;not null"`
	PackageVersion string          `gorm:"column:package_version;type:text;primaryKey;not null"`
	Language       string          `gorm:"column:language;type:text;primaryKey;not null"`
	Name           *string         `gorm:"column:name;type:text"`
	Audio          *string         `gorm:"column:audio;type:text"`
	DemoAudio      SingerDemoAudio `gorm:"foreignKey:DemoIndex,SingerID,PackageID,PackageVersion;references:Index,SingerID,PackageID,PackageVersion;constraint:OnDelete:CASCADE"`
}

func (SingerDemoAudioMultilingualInfo) TableName() string {
	return "singer_demo_audio_multilingual_info"
}

// Tables returns all database models in dependency order for AutoMigrate.
func Tables() []any {
	return []any{
		&Package{},
		&Dependency{},
		&Inference{},
		&Singer{},
		&SingerImport{},
		&PackageMultilingualInfo{},
		&InferenceMultilingualInfo{},
		&SingerMultilingualInfo{},
		&SingerDemoAudio{},
		&SingerDemoAudioMultilingualInfo{},
	}
}
