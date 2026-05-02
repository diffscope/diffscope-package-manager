package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	internallanguage "diffscope-package-manager/internal/language"
	"diffscope-package-manager/packagearchive"
	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
	"diffscope-package-manager/packageinfo"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

type infoOutput struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Data    infoData      `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type infoData struct {
	Type         string                 `json:"type"`
	Installation infoInstallationJSON   `json:"installation"`
	Package      inspectPackageJSON     `json:"package"`
	Dependencies []infoDependencyJSON   `json:"dependencies"`
	Inferences   []inspectInferenceJSON `json:"inferences,omitempty"`
	Singers      []infoSingerJSON       `json:"singers,omitempty"`
}

type infoInstallationJSON struct {
	Path        string `json:"path"`
	Hash        string `json:"hash"`
	InstalledAt string `json:"installedAt"`
}

type infoDependencyJSON struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    any    `json:"name,omitempty"`
}

type infoSingerJSON struct {
	ID         string                       `json:"id"`
	Class      string                       `json:"class"`
	Name       any                          `json:"name,omitempty"`
	Avatar     any                          `json:"avatar,omitempty"`
	Background any                          `json:"background,omitempty"`
	Imports    []infoImportJSON             `json:"imports"`
	DemoAudio  []inspectSingerDemoAudioJSON `json:"demoAudio,omitempty"`
}

type infoImportJSON struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	InferenceID string `json:"inferenceId"`
	Name        any    `json:"name,omitempty"`
}

type infoDependency struct {
	Reference packageinfo.PackageReference
	Name      *packageinfo.MultilingualText
}

type infoImport struct {
	Reference packageinfo.PackageReference
	Name      *packageinfo.MultilingualText
}

type infoPackage struct {
	Package      model.Package
	Inspection   packagearchive.PackageInspection
	Dependencies []infoDependency
	Imports      map[string][]infoImport
}

// NewInfoCmd creates the info command.
func NewInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <target>",
		Short: "Show installed package or module details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			languageCode, err := cmd.Flags().GetString("language")
			if err != nil {
				return err
			}
			return ShowInfo(args[0], viper.GetString("packages_dir"), languageCode, viper.GetBool("json"), cmd.OutOrStdout())
		},
	}

	cmd.Flags().String("language", "", "language tag or '*' for all languages")
	return cmd
}

// ShowInfo is the command entrypoint for showing installed package metadata.
func ShowInfo(target string, packagesDir string, languageCode string, jsonOutput bool, out io.Writer) error {
	reference, err := packageinfo.ParsePackageReference(target)
	if err != nil {
		return writeInfoError(jsonOutput, out, "SCHEMA_ERROR", err.Error(), nil, err)
	}
	if reference.PackageID == "" {
		err := fmt.Errorf("package query cannot be empty")
		return writeInfoError(jsonOutput, out, "SCHEMA_ERROR", err.Error(), nil, err)
	}

	if languageCode == "" {
		languageCode = internallanguage.CurrentCode()
	}

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		return writeInfoError(jsonOutput, out, "IO_ERROR", err.Error(), nil, err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	info, resolved, err := loadInfoPackage(db, reference)
	if err != nil {
		return writeInfoLoadError(jsonOutput, out, err)
	}
	if err := ensureInfoTargetExists(info, resolved); err != nil {
		return writeInfoError(jsonOutput, out, "NOT_FOUND", err.Error(), nil, err)
	}
	filterInfoTarget(&info, resolved)
	packageDir, err := filepath.Abs(filepath.Join(packagesDir, info.Package.Hash))
	if err != nil {
		return writeInfoError(jsonOutput, out, "IO_ERROR", fmt.Sprintf("resolve package installation path: %v", err), nil, err)
	}
	absolutizeInfoPaths(&info, packageDir)

	if jsonOutput {
		return json.NewEncoder(out).Encode(infoOutput{
			OK:      true,
			Command: "info",
			Data:    buildInfoData(info, resolved.Type, languageCode, packageDir),
		})
	}

	printInfoText(out, info, resolved.Type, languageCode, packageDir)
	return nil
}

func loadInfoPackage(db *gorm.DB, reference packageinfo.PackageReference) (infoPackage, packageinfo.PackageReference, error) {
	resolved, err := resolveInfoPackageVersion(db, reference)
	if err != nil {
		return infoPackage{}, packageinfo.PackageReference{}, err
	}

	var info infoPackage
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		info, err = loadInfoPackageInTx(tx, resolved)
		return err
	})
	if err != nil {
		return infoPackage{}, packageinfo.PackageReference{}, err
	}
	return info, resolved, nil
}

func resolveInfoPackageVersion(db *gorm.DB, reference packageinfo.PackageReference) (packageinfo.PackageReference, error) {
	if reference.Version != nil {
		return reference, nil
	}

	var packages []model.Package
	if err := db.
		Where("id = ?", reference.PackageID).
		Order("version ASC").
		Find(&packages).Error; err != nil {
		return packageinfo.PackageReference{}, err
	}
	if len(packages) == 0 {
		return packageinfo.PackageReference{}, infoNotFoundError{Message: fmt.Sprintf("package %q is not installed", reference.PackageID)}
	}
	if len(packages) > 1 {
		candidates := make([]string, 0, len(packages))
		for _, pkg := range packages {
			candidates = append(candidates, pkg.Version)
		}
		return packageinfo.PackageReference{}, infoAmbiguousVersionError{
			PackageID:  reference.PackageID,
			Candidates: candidates,
		}
	}

	version, err := packageinfo.ParsePackageVersion(packages[0].Version)
	if err != nil {
		return packageinfo.PackageReference{}, fmt.Errorf("parse installed version %q: %w", packages[0].Version, err)
	}
	reference.Version = infoPackageVersionPtr(version)
	return reference, nil
}

func loadInfoPackageInTx(db *gorm.DB, reference packageinfo.PackageReference) (infoPackage, error) {
	version := reference.Version.String()
	var pkg model.Package
	err := db.
		Where("id = ? AND version = ?", reference.PackageID, version).
		First(&pkg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return infoPackage{}, infoNotFoundError{Message: fmt.Sprintf("package %q is not installed", reference.String())}
	}
	if err != nil {
		return infoPackage{}, err
	}

	inspection := packagearchive.PackageInspection{
		Contributes: packagearchive.PackageInspectionContributions{},
		ID:          pkg.ID,
		URL:         stringValue(pkg.URL),
		Version:     *reference.Version,
	}

	packageTexts, err := loadPackageTexts(db, pkg.ID, pkg.Version)
	if err != nil {
		return infoPackage{}, err
	}
	inspection.Name = packageTexts.Name
	inspection.Description = packageTexts.Description
	inspection.Vendor = packageTexts.Vendor
	inspection.Readme = packageTexts.Readme
	inspection.License = packageTexts.License

	dependencies, err := loadInfoDependencies(db, pkg.ID, pkg.Version)
	if err != nil {
		return infoPackage{}, err
	}
	inspection.Dependencies = make([]packageinfo.PackageReference, 0, len(dependencies))
	for _, dependency := range dependencies {
		inspection.Dependencies = append(inspection.Dependencies, dependency.Reference)
	}

	inspection.Contributes.Inferences, err = loadInfoInferences(db, pkg.ID, pkg.Version)
	if err != nil {
		return infoPackage{}, err
	}

	singers, imports, err := loadInfoSingers(db, pkg.ID, pkg.Version)
	if err != nil {
		return infoPackage{}, err
	}
	inspection.Contributes.Singers = singers

	return infoPackage{
		Package:      pkg,
		Inspection:   inspection,
		Dependencies: dependencies,
		Imports:      imports,
	}, nil
}

type infoPackageTexts struct {
	Name        *packageinfo.MultilingualText
	Description *packageinfo.MultilingualText
	Vendor      *packageinfo.MultilingualText
	Readme      *packageinfo.MultilingualText
	License     *packageinfo.MultilingualText
}

func loadPackageTexts(db *gorm.DB, packageID string, version string) (infoPackageTexts, error) {
	var rows []model.PackageMultilingualInfo
	if err := db.
		Where("package_id = ? AND package_version = ?", packageID, version).
		Find(&rows).Error; err != nil {
		return infoPackageTexts{}, err
	}

	var names, descriptions, vendors, readmes, licenses packageinfo.MultilingualText
	for _, row := range rows {
		addOptionalMultilingualField(&names, row.Language, row.Name)
		addOptionalMultilingualField(&descriptions, row.Language, row.Description)
		addOptionalMultilingualField(&vendors, row.Language, row.Vendor)
		addOptionalMultilingualField(&readmes, row.Language, row.Readme)
		addOptionalMultilingualField(&licenses, row.Language, row.License)
	}

	return infoPackageTexts{
		Name:        normalizeMultilingualName(names),
		Description: normalizeMultilingualName(descriptions),
		Vendor:      normalizeMultilingualName(vendors),
		Readme:      normalizeMultilingualName(readmes),
		License:     normalizeMultilingualName(licenses),
	}, nil
}

func loadInfoDependencies(db *gorm.DB, packageID string, version string) ([]infoDependency, error) {
	var rows []model.Dependency
	if err := db.
		Where("package_id = ? AND package_version = ?", packageID, version).
		Order("dependent_package_id ASC, dependent_package_version ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	dependencies := make([]infoDependency, 0, len(rows))
	for _, row := range rows {
		dependencyVersion, err := packageinfo.ParsePackageVersion(row.DependentPackageVersion)
		if err != nil {
			return nil, fmt.Errorf("parse dependency version %q: %w", row.DependentPackageVersion, err)
		}
		name, err := packageName(db, row.DependentPackageID, row.DependentPackageVersion)
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, infoDependency{
			Reference: packageinfo.PackageReference{
				Type:      packageinfo.PackageReferenceTypePackage,
				PackageID: row.DependentPackageID,
				Version:   infoPackageVersionPtr(dependencyVersion),
			},
			Name: name,
		})
	}
	return dependencies, nil
}

func loadInfoInferences(db *gorm.DB, packageID string, version string) ([]packagearchive.InferenceInspection, error) {
	var rows []model.Inference
	if err := db.
		Where("package_id = ? AND package_version = ?", packageID, version).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	inferences := make([]packagearchive.InferenceInspection, 0, len(rows))
	for _, row := range rows {
		name, err := inferenceName(db, packageID, version, row.ID)
		if err != nil {
			return nil, err
		}
		inferences = append(inferences, packagearchive.InferenceInspection{
			ID:   row.ID,
			Name: name,
		})
	}
	return inferences, nil
}

func loadInfoSingers(db *gorm.DB, packageID string, version string) ([]packagearchive.SingerInspection, map[string][]infoImport, error) {
	var rows []model.Singer
	if err := db.
		Where("package_id = ? AND package_version = ?", packageID, version).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, nil, err
	}

	imports := make(map[string][]infoImport, len(rows))
	singers := make([]packagearchive.SingerInspection, 0, len(rows))
	for _, row := range rows {
		texts, err := loadSingerTexts(db, packageID, version, row.ID)
		if err != nil {
			return nil, nil, err
		}
		singerImports, err := loadInfoImports(db, row.ID, packageID, version)
		if err != nil {
			return nil, nil, err
		}
		demoAudio, err := loadInfoDemoAudio(db, row.ID, packageID, version)
		if err != nil {
			return nil, nil, err
		}

		references := make([]packageinfo.PackageReference, 0, len(singerImports))
		for _, imported := range singerImports {
			references = append(references, imported.Reference)
		}
		imports[row.ID] = singerImports
		singers = append(singers, packagearchive.SingerInspection{
			Avatar:     texts.Avatar,
			Background: texts.Background,
			Class:      row.Class,
			DemoAudio:  demoAudio,
			ID:         row.ID,
			Imports:    references,
			Name:       texts.Name,
		})
	}
	return singers, imports, nil
}

type infoSingerTexts struct {
	Name       *packageinfo.MultilingualText
	Avatar     *packageinfo.MultilingualText
	Background *packageinfo.MultilingualText
}

func loadSingerTexts(db *gorm.DB, packageID string, version string, singerID string) (infoSingerTexts, error) {
	var rows []model.SingerMultilingualInfo
	if err := db.
		Where("package_id = ? AND package_version = ? AND singer_id = ?", packageID, version, singerID).
		Find(&rows).Error; err != nil {
		return infoSingerTexts{}, err
	}

	var names, avatars, backgrounds packageinfo.MultilingualText
	for _, row := range rows {
		addOptionalMultilingualField(&names, row.Language, row.Name)
		addOptionalMultilingualField(&avatars, row.Language, row.Avatar)
		addOptionalMultilingualField(&backgrounds, row.Language, row.Background)
	}

	return infoSingerTexts{
		Name:       normalizeMultilingualName(names),
		Avatar:     normalizeMultilingualName(avatars),
		Background: normalizeMultilingualName(backgrounds),
	}, nil
}

func loadInfoImports(db *gorm.DB, singerID string, packageID string, version string) ([]infoImport, error) {
	var rows []model.SingerImport
	if err := db.
		Where("singer_id = ? AND package_id = ? AND package_version = ?", singerID, packageID, version).
		Order("imported_package_id ASC, imported_package_version ASC, imported_inference_id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	imports := make([]infoImport, 0, len(rows))
	for _, row := range rows {
		importedVersion, err := packageinfo.ParsePackageVersion(row.ImportedPackageVersion)
		if err != nil {
			return nil, fmt.Errorf("parse imported package version %q: %w", row.ImportedPackageVersion, err)
		}
		name, err := inferenceName(db, row.ImportedPackageID, row.ImportedPackageVersion, row.ImportedInferenceID)
		if err != nil {
			return nil, err
		}
		imports = append(imports, infoImport{
			Reference: packageinfo.PackageReference{
				Type:        packageinfo.PackageReferenceTypeInference,
				PackageID:   row.ImportedPackageID,
				Version:     infoPackageVersionPtr(importedVersion),
				InferenceID: row.ImportedInferenceID,
			},
			Name: name,
		})
	}
	return imports, nil
}

func loadInfoDemoAudio(db *gorm.DB, singerID string, packageID string, version string) ([]packagearchive.SingerDemoAudioInspection, error) {
	var rows []model.SingerDemoAudio
	if err := db.
		Where("singer_id = ? AND package_id = ? AND package_version = ?", singerID, packageID, version).
		Order("`index` ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	demoAudio := make([]packagearchive.SingerDemoAudioInspection, 0, len(rows))
	for _, row := range rows {
		texts, err := loadDemoAudioTexts(db, row.Index, singerID, packageID, version)
		if err != nil {
			return nil, err
		}
		demoAudio = append(demoAudio, texts)
	}
	return demoAudio, nil
}

func loadDemoAudioTexts(db *gorm.DB, demoIndex int, singerID string, packageID string, version string) (packagearchive.SingerDemoAudioInspection, error) {
	var rows []model.SingerDemoAudioMultilingualInfo
	if err := db.
		Where("demo_index = ? AND singer_id = ? AND package_id = ? AND package_version = ?", demoIndex, singerID, packageID, version).
		Find(&rows).Error; err != nil {
		return packagearchive.SingerDemoAudioInspection{}, err
	}

	var names, audios packageinfo.MultilingualText
	for _, row := range rows {
		addOptionalMultilingualField(&names, row.Language, row.Name)
		addOptionalMultilingualField(&audios, row.Language, row.Audio)
	}
	name := normalizeMultilingualName(names)
	audio := normalizeMultilingualName(audios)
	if name == nil {
		name = &packageinfo.MultilingualText{}
	}
	if audio == nil {
		audio = &packageinfo.MultilingualText{}
	}
	return packagearchive.SingerDemoAudioInspection{
		Name:  *name,
		Audio: *audio,
	}, nil
}

func ensureInfoTargetExists(info infoPackage, reference packageinfo.PackageReference) error {
	switch reference.Type {
	case packageinfo.PackageReferenceTypeInference:
		for _, inference := range info.Inspection.Contributes.Inferences {
			if inference.ID == reference.InferenceID {
				return nil
			}
		}
		return fmt.Errorf("inference %q is not installed in %s", reference.InferenceID, packageReferenceBase(reference))
	case packageinfo.PackageReferenceTypeSinger:
		for _, singer := range info.Inspection.Contributes.Singers {
			if singer.ID == reference.SingerID {
				return nil
			}
		}
		return fmt.Errorf("singer %q is not installed in %s", reference.SingerID, packageReferenceBase(reference))
	default:
		return nil
	}
}

func filterInfoTarget(info *infoPackage, reference packageinfo.PackageReference) {
	switch reference.Type {
	case packageinfo.PackageReferenceTypeInference:
		filtered := info.Inspection.Contributes.Inferences[:0]
		for _, inference := range info.Inspection.Contributes.Inferences {
			if inference.ID == reference.InferenceID {
				filtered = append(filtered, inference)
			}
		}
		info.Inspection.Contributes.Inferences = filtered
		info.Inspection.Contributes.Singers = nil
		info.Imports = nil
	case packageinfo.PackageReferenceTypeSinger:
		filtered := info.Inspection.Contributes.Singers[:0]
		for _, singer := range info.Inspection.Contributes.Singers {
			if singer.ID == reference.SingerID {
				filtered = append(filtered, singer)
			}
		}
		info.Inspection.Contributes.Inferences = nil
		info.Inspection.Contributes.Singers = filtered
	}
}

func absolutizeInfoPaths(info *infoPackage, packageDir string) {
	absolutizeMultilingualPath(info.Inspection.Readme, packageDir)
	absolutizeMultilingualPath(info.Inspection.License, packageDir)
	for singerIndex := range info.Inspection.Contributes.Singers {
		singer := &info.Inspection.Contributes.Singers[singerIndex]
		absolutizeMultilingualPath(singer.Avatar, packageDir)
		absolutizeMultilingualPath(singer.Background, packageDir)
		for demoIndex := range singer.DemoAudio {
			absolutizeMultilingualPath(&singer.DemoAudio[demoIndex].Audio, packageDir)
		}
	}
}

func absolutizeMultilingualPath(text *packageinfo.MultilingualText, packageDir string) {
	if text == nil {
		return
	}
	text.Default = absolutizeInfoPath(text.Default, packageDir)
	for language, value := range text.Texts {
		text.Texts[language] = absolutizeInfoPath(value, packageDir)
	}
}

func absolutizeInfoPath(value string, packageDir string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(packageDir, value)
}

func buildInfoData(info infoPackage, targetType packageinfo.PackageReferenceType, languageCode string, packageDir string) infoData {
	data := infoData{
		Type: targetTypeText(targetType),
		Installation: infoInstallationJSON{
			Path:        packageDir,
			Hash:        info.Package.Hash,
			InstalledAt: formatInstalledAtJSON(info.Package.InstalledAt),
		},
		Package: inspectPackageJSON{
			ID:          info.Inspection.ID,
			Version:     info.Inspection.Version.String(),
			URL:         info.Inspection.URL,
			Name:        multilingualJSONValue(info.Inspection.Name, languageCode),
			Description: multilingualJSONValue(info.Inspection.Description, languageCode),
			Vendor:      multilingualJSONValue(info.Inspection.Vendor, languageCode),
			Readme:      multilingualJSONValue(info.Inspection.Readme, languageCode),
			License:     multilingualJSONValue(info.Inspection.License, languageCode),
		},
		Dependencies: make([]infoDependencyJSON, 0, len(info.Dependencies)),
		Inferences:   make([]inspectInferenceJSON, 0, len(info.Inspection.Contributes.Inferences)),
		Singers:      make([]infoSingerJSON, 0, len(info.Inspection.Contributes.Singers)),
	}

	for _, dependency := range info.Dependencies {
		data.Dependencies = append(data.Dependencies, infoDependencyJSON{
			ID:      dependency.Reference.PackageID,
			Version: dependency.Reference.Version.String(),
			Name:    multilingualJSONValue(dependency.Name, languageCode),
		})
	}

	for _, inference := range info.Inspection.Contributes.Inferences {
		data.Inferences = append(data.Inferences, inspectInferenceJSON{
			ID:   inference.ID,
			Name: multilingualJSONValue(inference.Name, languageCode),
		})
	}

	for _, singer := range info.Inspection.Contributes.Singers {
		item := infoSingerJSON{
			ID:         singer.ID,
			Class:      singer.Class,
			Name:       multilingualJSONValue(singer.Name, languageCode),
			Avatar:     multilingualJSONValue(singer.Avatar, languageCode),
			Background: multilingualJSONValue(singer.Background, languageCode),
			Imports:    make([]infoImportJSON, 0, len(info.Imports[singer.ID])),
			DemoAudio:  make([]inspectSingerDemoAudioJSON, 0, len(singer.DemoAudio)),
		}
		for _, imported := range info.Imports[singer.ID] {
			item.Imports = append(item.Imports, infoImportJSON{
				ID:          imported.Reference.PackageID,
				Version:     imported.Reference.Version.String(),
				InferenceID: imported.Reference.InferenceID,
				Name:        multilingualJSONValue(imported.Name, languageCode),
			})
		}
		for index, demoAudio := range singer.DemoAudio {
			item.DemoAudio = append(item.DemoAudio, inspectSingerDemoAudioJSON{
				Index: index + 1,
				Name:  multilingualJSONValue(&demoAudio.Name, languageCode),
				Audio: multilingualJSONValue(&demoAudio.Audio, languageCode),
			})
		}
		data.Singers = append(data.Singers, item)
	}

	return data
}

func printInfoText(out io.Writer, info infoPackage, targetType packageinfo.PackageReferenceType, languageCode string, packageDir string) {
	printSectionTitle(out, "Installation")
	printField(out, "  ", "Path", packageDir)
	printField(out, "  ", "Hash", info.Package.Hash)
	printField(out, "  ", "InstalledAt", formatInstalledAtText(info.Package.InstalledAt))
	fmt.Fprintln(out)

	printSectionTitle(out, "Package")
	printField(out, "  ", "ID", info.Inspection.ID)
	printField(out, "  ", "Version", info.Inspection.Version.String())
	printOptionalText(out, "  Name", info.Inspection.Name, languageCode)
	printOptionalText(out, "  Description", info.Inspection.Description, languageCode)
	printOptionalText(out, "  Vendor", info.Inspection.Vendor, languageCode)
	printOptionalText(out, "  Readme", info.Inspection.Readme, languageCode)
	printOptionalText(out, "  License", info.Inspection.License, languageCode)
	if info.Inspection.URL != "" {
		printField(out, "  ", "URL", info.Inspection.URL)
	}
	fmt.Fprintln(out)

	printSectionTitle(out, "Dependencies")
	if len(info.Dependencies) == 0 {
		printEmpty(out, "  ")
	}
	for _, dependency := range info.Dependencies {
		fmt.Fprintf(out, "  %s\n", referenceDisplay(dependency.Name, dependency.Reference, languageCode))
	}
	fmt.Fprintln(out)

	if targetType != packageinfo.PackageReferenceTypeSinger {
		printSectionTitle(out, "Inferences")
		if len(info.Inspection.Contributes.Inferences) == 0 {
			printEmpty(out, "  ")
		}
		for _, inference := range info.Inspection.Contributes.Inferences {
			fmt.Fprintf(out, "  %s\n", inference.ID)
			printOptionalText(out, "    Name", inference.Name, languageCode)
		}
		fmt.Fprintln(out)
	}

	if targetType != packageinfo.PackageReferenceTypeInference {
		printSectionTitle(out, "Singers")
		if len(info.Inspection.Contributes.Singers) == 0 {
			printEmpty(out, "  ")
		}
		for _, singer := range info.Inspection.Contributes.Singers {
			fmt.Fprintf(out, "  %s\n", singer.ID)
			printOptionalText(out, "    Name", singer.Name, languageCode)
			printField(out, "    ", "Class", singer.Class)
			printOptionalText(out, "    Avatar", singer.Avatar, languageCode)
			printOptionalText(out, "    Background", singer.Background, languageCode)

			printSubsectionLabel(out, "    ", "Imports")
			if len(info.Imports[singer.ID]) == 0 {
				printEmpty(out, "      ")
			}
			for _, imported := range info.Imports[singer.ID] {
				fmt.Fprintf(out, "      %s\n", referenceDisplay(imported.Name, imported.Reference, languageCode))
			}

			if len(singer.DemoAudio) > 0 {
				printSubsectionLabel(out, "    ", "DemoAudio")
				for index, demoAudio := range singer.DemoAudio {
					fmt.Fprintf(out, "      #%d\n", index+1)
					printRequiredText(out, "        Name", &demoAudio.Name, languageCode)
					printRequiredText(out, "        Audio", &demoAudio.Audio, languageCode)
				}
			}
		}
	}
}

func addOptionalMultilingualField(text *packageinfo.MultilingualText, language string, value *string) {
	if value == nil {
		return
	}
	if text.Texts == nil {
		text.Texts = make(map[string]string)
	}
	addMultilingualName(text, language, *value)
}

func targetTypeText(targetType packageinfo.PackageReferenceType) string {
	switch targetType {
	case packageinfo.PackageReferenceTypeInference:
		return "inference"
	case packageinfo.PackageReferenceTypeSinger:
		return "singer"
	default:
		return "package"
	}
}

func packageReferenceBase(reference packageinfo.PackageReference) string {
	if reference.Version == nil {
		return reference.PackageID
	}
	return reference.PackageID + "@" + reference.Version.String()
}

func infoPackageVersionPtr(version packageinfo.PackageVersion) *packageinfo.PackageVersion {
	copied := version
	return &copied
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type infoNotFoundError struct {
	Message string
}

func (e infoNotFoundError) Error() string {
	return e.Message
}

type infoAmbiguousVersionError struct {
	PackageID  string
	Candidates []string
}

func (e infoAmbiguousVersionError) Error() string {
	candidates := append([]string(nil), e.Candidates...)
	sort.Strings(candidates)
	return fmt.Sprintf("ambiguous version for package %q: %v", e.PackageID, candidates)
}

func writeInfoLoadError(jsonOutput bool, out io.Writer, err error) error {
	var ambiguous infoAmbiguousVersionError
	if errors.As(err, &ambiguous) {
		details := map[string]any{
			"package":    ambiguous.PackageID,
			"candidates": ambiguous.Candidates,
		}
		return writeInfoError(jsonOutput, out, "AMBIGUOUS_VERSION", ambiguous.Error(), details, err)
	}

	var notFound infoNotFoundError
	if errors.As(err, &notFound) {
		return writeInfoError(jsonOutput, out, "NOT_FOUND", notFound.Error(), nil, err)
	}

	return writeInfoError(jsonOutput, out, "IO_ERROR", err.Error(), nil, err)
}

func writeInfoError(jsonOutput bool, out io.Writer, code string, message string, details map[string]any, err error) error {
	if jsonOutput {
		encodeErr := json.NewEncoder(out).Encode(infoOutput{
			OK:      false,
			Command: "info",
			Error: &inspectError{
				Code:    code,
				Message: message,
				Details: details,
			},
		})
		if encodeErr != nil {
			return encodeErr
		}
	}
	return err
}
