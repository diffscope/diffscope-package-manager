package packagearchive

import (
	"encoding/json"
	"fmt"

	"diffscope-package-manager/packageinfo"

	"github.com/go-playground/validator/v10"
)

const packageDescriptionPath = "desc.json"

// PackageInspection stores package metadata extracted from an archive.
//
// It mirrors packageinfo.PackageDescription for package-level fields, but
// expands contributed description file paths into database-oriented metadata.
type PackageInspection struct {
	Contributes  PackageInspectionContributions
	Dependencies []packageinfo.PackageReference
	Description  *packageinfo.MultilingualText
	ID           string
	License      *packageinfo.MultilingualText
	Name         *packageinfo.MultilingualText
	Readme       *packageinfo.MultilingualText
	URL          string
	Vendor       *packageinfo.MultilingualText
	Version      packageinfo.PackageVersion
}

// PackageInspectionContributions stores contributed resources after expanding
// package-local description file paths.
type PackageInspectionContributions struct {
	Inferences []InferenceInspection
	Singers    []SingerInspection
}

// InferenceInspection stores the inference fields represented in the package database.
type InferenceInspection struct {
	ID   string
	Name *packageinfo.MultilingualText
}

// SingerInspection stores the singer fields represented in the package database.
type SingerInspection struct {
	Avatar     *packageinfo.MultilingualText
	Background *packageinfo.MultilingualText
	Class      string
	DemoAudio  []SingerDemoAudioInspection
	ID         string
	Imports    []packageinfo.PackageReference
	Name       *packageinfo.MultilingualText
}

// SingerDemoAudioInspection stores demo audio metadata represented in the package database.
type SingerDemoAudioInspection struct {
	Name  packageinfo.MultilingualText
	Audio packageinfo.MultilingualText
}

// InspectPackage reads package metadata from a zip archive without installing it.
func InspectPackage(reader ZipReader) (PackageInspection, error) {
	archive, err := openPackageZip(reader)
	if err != nil {
		return PackageInspection{}, err
	}

	files, err := indexPackageZipFiles(archive)
	if err != nil {
		return PackageInspection{}, err
	}

	validate := validator.New()
	if err := packageinfo.RegisterValidator(validate); err != nil {
		return PackageInspection{}, fmt.Errorf("register package validators: %w", err)
	}

	var description packageinfo.PackageDescription
	if err := readJSONFile(files, packageDescriptionPath, &description); err != nil {
		return PackageInspection{}, err
	}
	if err := normalizePackageDescriptionPaths(packageDescriptionPath, &description); err != nil {
		return PackageInspection{}, fmt.Errorf("normalize paths in %s: %w", packageDescriptionPath, err)
	}
	if err := validate.Struct(description); err != nil {
		return PackageInspection{}, fmt.Errorf("validate %s: %w", packageDescriptionPath, err)
	}

	inspection := PackageInspection{
		Contributes: PackageInspectionContributions{
			Inferences: make([]InferenceInspection, 0, len(description.Contributes.Inferences)),
			Singers:    make([]SingerInspection, 0, len(description.Contributes.Singers)),
		},
		Dependencies: packageDependencyReferences(description.Dependencies),
		Description:  description.Description,
		ID:           description.ID,
		License:      description.License,
		Name:         description.Name,
		Readme:       description.Readme,
		URL:          description.URL,
		Vendor:       description.Vendor,
		Version:      description.Version,
	}

	inferenceIDs := make(map[string]struct{}, len(description.Contributes.Inferences))
	for _, inferencePath := range description.Contributes.Inferences {
		inference, err := readInferenceInspection(files, inferencePath, validate)
		if err != nil {
			return PackageInspection{}, err
		}
		inspection.Contributes.Inferences = append(inspection.Contributes.Inferences, inference)
		inferenceIDs[inference.ID] = struct{}{}
	}

	for _, singerPath := range description.Contributes.Singers {
		singer, err := readSingerInspection(files, singerPath, validate, description, inferenceIDs)
		if err != nil {
			return PackageInspection{}, err
		}
		inspection.Contributes.Singers = append(inspection.Contributes.Singers, singer)
	}

	return inspection, nil
}

func readInferenceInspection(files packageZipFiles, filePath string, validate *validator.Validate) (InferenceInspection, error) {
	var description packageinfo.InferenceDescription
	if err := readJSONFile(files, filePath, &description); err != nil {
		return InferenceInspection{}, err
	}
	if err := validate.Struct(description); err != nil {
		return InferenceInspection{}, fmt.Errorf("validate %q: %w", filePath, err)
	}

	return InferenceInspection{
		ID:   description.ID,
		Name: description.Name,
	}, nil
}

func readSingerInspection(
	files packageZipFiles,
	filePath string,
	validate *validator.Validate,
	packageDescription packageinfo.PackageDescription,
	inferenceIDs map[string]struct{},
) (SingerInspection, error) {
	var description packageinfo.SingerDescription
	if err := readJSONFile(files, filePath, &description); err != nil {
		return SingerInspection{}, err
	}
	if err := normalizeSingerDescriptionPaths(filePath, &description); err != nil {
		return SingerInspection{}, fmt.Errorf("normalize paths in %q: %w", filePath, err)
	}
	if err := validate.Struct(description); err != nil {
		return SingerInspection{}, fmt.Errorf("validate %q: %w", filePath, err)
	}

	demoAudio := make([]SingerDemoAudioInspection, 0, len(description.DemoAudio))
	for _, item := range description.DemoAudio {
		demoAudio = append(demoAudio, SingerDemoAudioInspection{
			Name:  item.Name,
			Audio: item.Path,
		})
	}

	imports := make([]packageinfo.PackageReference, 0, len(description.Imports))
	for index, item := range description.Imports {
		reference, err := resolveSingerInferenceImport(packageDescription, inferenceIDs, item)
		if err != nil {
			return SingerInspection{}, fmt.Errorf("resolve import %d in %q: %w", index, filePath, err)
		}
		imports = append(imports, reference)
	}

	return SingerInspection{
		Avatar:     description.Avatar,
		Background: description.Background,
		Class:      description.Class,
		DemoAudio:  demoAudio,
		ID:         description.ID,
		Imports:    imports,
		Name:       description.Name,
	}, nil
}

func resolveSingerInferenceImport(
	packageDescription packageinfo.PackageDescription,
	inferenceIDs map[string]struct{},
	item packageinfo.SingerInferenceImport,
) (packageinfo.PackageReference, error) {
	var packageID string
	var version packageinfo.PackageVersion

	switch {
	case item.ID == "":
		if item.Version != nil {
			return packageinfo.PackageReference{}, fmt.Errorf("version cannot be specified without package id")
		}
		packageID = packageDescription.ID
		version = packageDescription.Version
	case item.Version == nil:
		dependencyVersion, ok := latestDependencyVersion(packageDescription.Dependencies, item.ID)
		if !ok {
			return packageinfo.PackageReference{}, fmt.Errorf("dependency %q not found", item.ID)
		}
		packageID = item.ID
		version = dependencyVersion
	default:
		if !isCurrentPackage(packageDescription, item.ID, *item.Version) && !hasDependency(packageDescription.Dependencies, item.ID, *item.Version) {
			return packageinfo.PackageReference{}, fmt.Errorf("dependency %q@%s not found", item.ID, item.Version.String())
		}
		packageID = item.ID
		version = *item.Version
	}

	return packageinfo.PackageReference{
		Type:        packageinfo.PackageReferenceTypeInference,
		PackageID:   packageID,
		Version:     packageVersionPtr(version),
		InferenceID: item.InferenceID,
	}, nil
}

func packageDependencyReferences(dependencies []packageinfo.PackageDependency) []packageinfo.PackageReference {
	references := make([]packageinfo.PackageReference, 0, len(dependencies))
	for _, dependency := range dependencies {
		references = append(references, packageinfo.PackageReference{
			Type:      packageinfo.PackageReferenceTypePackage,
			PackageID: dependency.ID,
			Version:   packageVersionPtr(dependency.Version),
		})
	}
	return references
}

func latestDependencyVersion(dependencies []packageinfo.PackageDependency, packageID string) (packageinfo.PackageVersion, bool) {
	var latest packageinfo.PackageVersion
	found := false
	for _, dependency := range dependencies {
		if dependency.ID != packageID {
			continue
		}
		if !found || latest.Compare(dependency.Version) < 0 {
			latest = dependency.Version
			found = true
		}
	}
	return latest, found
}

func hasDependency(dependencies []packageinfo.PackageDependency, packageID string, version packageinfo.PackageVersion) bool {
	for _, dependency := range dependencies {
		if dependency.ID == packageID && dependency.Version.Compare(version) == 0 {
			return true
		}
	}
	return false
}

func isCurrentPackage(packageDescription packageinfo.PackageDescription, packageID string, version packageinfo.PackageVersion) bool {
	return packageDescription.ID == packageID && packageDescription.Version.Compare(version) == 0
}

func packageVersionPtr(version packageinfo.PackageVersion) *packageinfo.PackageVersion {
	copied := version
	return &copied
}

func readJSONFile(files packageZipFiles, filePath string, value any) error {
	data, err := readPackageZipFile(files, filePath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("parse zip entry %q: %w", filePath, err)
	}
	return nil
}
