package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internallanguage "diffscope-package-manager/internal/language"
	"diffscope-package-manager/packagearchive"
	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
	"diffscope-package-manager/packageinfo"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

var (
	inspectSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("6"))
	inspectKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))
	inspectEmptyStyle = lipgloss.NewStyle().
				Faint(true)
	inspectOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))
	inspectWarningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3"))
	inspectErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1"))
)

type packageFileReader struct {
	*os.File
	size int64
}

func (r packageFileReader) Size() int64 {
	return r.size
}

type inspectOutput struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Data    inspectData   `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type inspectError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type inspectData struct {
	File         inspectFileJSON         `json:"file"`
	Package      inspectPackageJSON      `json:"package"`
	Dependencies []inspectDependencyJSON `json:"dependencies"`
	Inferences   []inspectInferenceJSON  `json:"inferences"`
	Singers      []inspectSingerJSON     `json:"singers"`
}

type inspectFileJSON struct {
	Path string `json:"path"`
	Hash string `json:"hash,omitempty"`
}

type inspectPackageJSON struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	URL         string `json:"url,omitempty"`
	Name        any    `json:"name,omitempty"`
	Description any    `json:"description,omitempty"`
	Vendor      any    `json:"vendor,omitempty"`
	Readme      any    `json:"readme,omitempty"`
	License     any    `json:"license,omitempty"`
}

type inspectDependencyJSON struct {
	Reference string `json:"reference"`
	Installed bool   `json:"installed"`
	Status    string `json:"status"`
}

type inspectInferenceJSON struct {
	ID   string `json:"id"`
	Name any    `json:"name,omitempty"`
}

type inspectSingerJSON struct {
	ID         string                       `json:"id"`
	Class      string                       `json:"class"`
	Name       any                          `json:"name,omitempty"`
	Avatar     any                          `json:"avatar,omitempty"`
	Background any                          `json:"background,omitempty"`
	Imports    []inspectImportJSON          `json:"imports"`
	DemoAudio  []inspectSingerDemoAudioJSON `json:"demoAudio,omitempty"`
}

type inspectImportJSON struct {
	Reference          string `json:"reference"`
	PackageInstalled   bool   `json:"packageInstalled"`
	InferenceInstalled bool   `json:"inferenceInstalled"`
	Status             string `json:"status"`
}

type inspectSingerDemoAudioJSON struct {
	Index int `json:"index"`
	Name  any `json:"name"`
	Audio any `json:"audio"`
}

type inspectDependencyStatus struct {
	Reference packageinfo.PackageReference
	Installed bool
}

type inspectImportStatus struct {
	Reference          packageinfo.PackageReference
	PackageInstalled   bool
	InferenceInstalled bool
}

type inspectStatus struct {
	Dependencies []inspectDependencyStatus
	Imports      map[string][]inspectImportStatus
}

// NewInspectCmd creates the inspect command.
func NewInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <packageFile>",
		Short: "Inspect a package file without installing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			languageCode, err := cmd.Flags().GetString("language")
			if err != nil {
				return err
			}
			includeHash, err := cmd.Flags().GetBool("hash")
			if err != nil {
				return err
			}
			return InspectPackageFile(args[0], viper.GetString("packages_dir"), languageCode, viper.GetBool("json"), includeHash, cmd.OutOrStdout())
		},
	}

	cmd.Flags().String("language", "", "language tag or '*' for all languages")
	cmd.Flags().Bool("hash", false, "include package hash in output")
	return cmd
}

// InspectPackageFile is the command entrypoint for inspecting a package archive.
func InspectPackageFile(packageFilePath string, packagesDir string, languageCode string, jsonOutput bool, includeHash bool, out io.Writer) error {
	absolutePackageFilePath, err := filepath.Abs(packageFilePath)
	if err != nil {
		return writeInspectError(jsonOutput, out, "IO_ERROR", fmt.Sprintf("resolve package file path: %v", err), err)
	}

	reader, err := openPackageFileReader(absolutePackageFilePath)
	if err != nil {
		return writeInspectError(jsonOutput, out, "IO_ERROR", err.Error(), err)
	}
	defer reader.Close()

	inspection, err := packagearchive.InspectPackage(reader)
	if err != nil {
		return writeInspectError(jsonOutput, out, "SCHEMA_ERROR", err.Error(), err)
	}

	var packageHash string
	if includeHash {
		hash, err := packagearchive.ComputePackageHash(reader)
		if err != nil {
			return writeInspectError(jsonOutput, out, "IO_ERROR", err.Error(), err)
		}
		packageHash = fmt.Sprintf("%x", hash)
	}

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		return writeInspectError(jsonOutput, out, "IO_ERROR", err.Error(), err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	status, err := inspectInstalledStatus(db, inspection)
	if err != nil {
		return writeInspectError(jsonOutput, out, "IO_ERROR", err.Error(), err)
	}

	if languageCode == "" {
		languageCode = internallanguage.CurrentCode()
	}

	data := buildInspectData(absolutePackageFilePath, inspection, status, languageCode, packageHash)
	if jsonOutput {
		return json.NewEncoder(out).Encode(inspectOutput{
			OK:      true,
			Command: "inspect",
			Data:    data,
		})
	}

	printInspectText(out, inspection, status, absolutePackageFilePath, packageHash, languageCode)
	return nil
}

func openPackageFileReader(packageFilePath string) (packageFileReader, error) {
	file, err := os.Open(packageFilePath)
	if err != nil {
		return packageFileReader{}, fmt.Errorf("open package file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return packageFileReader{}, fmt.Errorf("stat package file: %w", err)
	}
	if info.IsDir() {
		file.Close()
		return packageFileReader{}, fmt.Errorf("package file is a directory: %s", packageFilePath)
	}

	return packageFileReader{File: file, size: info.Size()}, nil
}

func inspectInstalledStatus(db *gorm.DB, inspection packagearchive.PackageInspection) (inspectStatus, error) {
	status := inspectStatus{
		Dependencies: make([]inspectDependencyStatus, 0, len(inspection.Dependencies)),
		Imports:      make(map[string][]inspectImportStatus, len(inspection.Contributes.Singers)),
	}
	currentInferences := make(map[string]struct{}, len(inspection.Contributes.Inferences))
	for _, inference := range inspection.Contributes.Inferences {
		currentInferences[inference.ID] = struct{}{}
	}

	for _, dependency := range inspection.Dependencies {
		if dependency.Type != packageinfo.PackageReferenceTypePackage || dependency.Version == nil {
			panic("inspect dependency reference must be a package with version")
		}
		installed, err := packageInstalled(db, dependency.PackageID, dependency.Version.String())
		if err != nil {
			return inspectStatus{}, err
		}
		status.Dependencies = append(status.Dependencies, inspectDependencyStatus{
			Reference: dependency,
			Installed: installed,
		})
	}

	for _, singer := range inspection.Contributes.Singers {
		imports := make([]inspectImportStatus, 0, len(singer.Imports))
		for _, imported := range singer.Imports {
			if imported.Type != packageinfo.PackageReferenceTypeInference || imported.Version == nil {
				panic("inspect singer import reference must be an inference with version")
			}

			if isCurrentPackageImport(inspection, imported) {
				_, installedInference := currentInferences[imported.InferenceID]
				imports = append(imports, inspectImportStatus{
					Reference:          imported,
					PackageInstalled:   true,
					InferenceInstalled: installedInference,
				})
				continue
			}

			installedPackage, err := packageInstalled(db, imported.PackageID, imported.Version.String())
			if err != nil {
				return inspectStatus{}, err
			}

			installedInference := false
			if installedPackage {
				installedInference, err = inferenceInstalled(db, imported.PackageID, imported.Version.String(), imported.InferenceID)
				if err != nil {
					return inspectStatus{}, err
				}
			}

			imports = append(imports, inspectImportStatus{
				Reference:          imported,
				PackageInstalled:   installedPackage,
				InferenceInstalled: installedInference,
			})
		}
		status.Imports[singer.ID] = imports
	}

	return status, nil
}

func isCurrentPackageImport(inspection packagearchive.PackageInspection, imported packageinfo.PackageReference) bool {
	return imported.PackageID == inspection.ID &&
		imported.Version != nil &&
		imported.Version.Compare(inspection.Version) == 0
}

func packageInstalled(db *gorm.DB, packageID string, version string) (bool, error) {
	var count int64
	err := db.Model(&model.Package{}).
		Where("id = ? AND version = ?", packageID, version).
		Count(&count).Error
	return count > 0, err
}

func inferenceInstalled(db *gorm.DB, packageID string, version string, inferenceID string) (bool, error) {
	var count int64
	err := db.Model(&model.Inference{}).
		Where("package_id = ? AND package_version = ? AND id = ?", packageID, version, inferenceID).
		Count(&count).Error
	return count > 0, err
}

func buildInspectData(packageFilePath string, inspection packagearchive.PackageInspection, status inspectStatus, languageCode string, packageHash string) inspectData {
	data := inspectData{
		File: inspectFileJSON{
			Path: packageFilePath,
			Hash: packageHash,
		},
		Package: inspectPackageJSON{
			ID:          inspection.ID,
			Version:     inspection.Version.String(),
			URL:         inspection.URL,
			Name:        multilingualJSONValue(inspection.Name, languageCode),
			Description: multilingualJSONValue(inspection.Description, languageCode),
			Vendor:      multilingualJSONValue(inspection.Vendor, languageCode),
			Readme:      multilingualJSONValue(inspection.Readme, languageCode),
			License:     multilingualJSONValue(inspection.License, languageCode),
		},
		Dependencies: make([]inspectDependencyJSON, 0, len(status.Dependencies)),
		Inferences:   make([]inspectInferenceJSON, 0, len(inspection.Contributes.Inferences)),
		Singers:      make([]inspectSingerJSON, 0, len(inspection.Contributes.Singers)),
	}

	for _, dependency := range status.Dependencies {
		data.Dependencies = append(data.Dependencies, inspectDependencyJSON{
			Reference: dependency.Reference.String(),
			Installed: dependency.Installed,
			Status:    dependencyStatusText(dependency),
		})
	}

	for _, inference := range inspection.Contributes.Inferences {
		data.Inferences = append(data.Inferences, inspectInferenceJSON{
			ID:   inference.ID,
			Name: multilingualJSONValue(inference.Name, languageCode),
		})
	}

	for _, singer := range inspection.Contributes.Singers {
		item := inspectSingerJSON{
			ID:         singer.ID,
			Class:      singer.Class,
			Name:       multilingualJSONValue(singer.Name, languageCode),
			Avatar:     multilingualJSONValue(singer.Avatar, languageCode),
			Background: multilingualJSONValue(singer.Background, languageCode),
			Imports:    make([]inspectImportJSON, 0, len(status.Imports[singer.ID])),
			DemoAudio:  make([]inspectSingerDemoAudioJSON, 0, len(singer.DemoAudio)),
		}

		for _, imported := range status.Imports[singer.ID] {
			item.Imports = append(item.Imports, inspectImportJSON{
				Reference:          imported.Reference.String(),
				PackageInstalled:   imported.PackageInstalled,
				InferenceInstalled: imported.InferenceInstalled,
				Status:             importStatusText(imported),
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

func printInspectText(out io.Writer, inspection packagearchive.PackageInspection, status inspectStatus, packageFilePath string, packageHash string, languageCode string) {
	if languageCode == "" {
		languageCode = internallanguage.CurrentCode()
	}

	printSectionTitle(out, "File")
	printField(out, "  ", "Path", packageFilePath)
	if packageHash != "" {
		printField(out, "  ", "Hash", packageHash)
	}
	fmt.Fprintln(out)

	printSectionTitle(out, "Package")
	printField(out, "  ", "ID", inspection.ID)
	printField(out, "  ", "Version", inspection.Version.String())
	printOptionalText(out, "  Name", inspection.Name, languageCode)
	printOptionalText(out, "  Description", inspection.Description, languageCode)
	printOptionalText(out, "  Vendor", inspection.Vendor, languageCode)
	printOptionalText(out, "  Readme", inspection.Readme, languageCode)
	printOptionalText(out, "  License", inspection.License, languageCode)
	if inspection.URL != "" {
		printField(out, "  ", "URL", inspection.URL)
	}
	fmt.Fprintln(out)

	printSectionTitle(out, "Dependencies")
	if len(status.Dependencies) == 0 {
		printEmpty(out, "  ")
	}
	for _, dependency := range status.Dependencies {
		fmt.Fprintf(
			out,
			"  %s  %s\n",
			dependency.Reference.String(),
			dependencyStatusLabel(dependency),
		)
	}
	fmt.Fprintln(out)

	printSectionTitle(out, "Inferences")
	if len(inspection.Contributes.Inferences) == 0 {
		printEmpty(out, "  ")
	}
	for _, inference := range inspection.Contributes.Inferences {
		fmt.Fprintf(out, "  %s\n", inference.ID)
		printOptionalText(out, "    Name", inference.Name, languageCode)
	}
	fmt.Fprintln(out)

	printSectionTitle(out, "Singers")
	if len(inspection.Contributes.Singers) == 0 {
		printEmpty(out, "  ")
	}
	for _, singer := range inspection.Contributes.Singers {
		fmt.Fprintf(out, "  %s\n", singer.ID)
		printOptionalText(out, "    Name", singer.Name, languageCode)
		printField(out, "    ", "Class", singer.Class)
		printOptionalText(out, "    Avatar", singer.Avatar, languageCode)
		printOptionalText(out, "    Background", singer.Background, languageCode)

		printSubsectionLabel(out, "    ", "Imports")
		if len(status.Imports[singer.ID]) == 0 {
			printEmpty(out, "      ")
		}
		for _, imported := range status.Imports[singer.ID] {
			fmt.Fprintf(
				out,
				"      %s  %s\n",
				imported.Reference.String(),
				importStatusLabel(imported),
			)
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

func printOptionalText(out io.Writer, label string, text *packageinfo.MultilingualText, languageCode string) {
	if text == nil {
		return
	}
	printRequiredText(out, label, text, languageCode)
}

func printRequiredText(out io.Writer, label string, text *packageinfo.MultilingualText, languageCode string) {
	if languageCode == "*" {
		values := multilingualTextMap(text)
		indent, labelKey := splitTextLabel(label)
		for _, languageKey := range sortedMultilingualKeys(text) {
			printField(out, indent, fmt.Sprintf("%s[%s]", labelKey, languageKey), values[languageKey])
		}
		return
	}
	indent, key := splitTextLabel(label)
	printField(out, indent, key, selectMultilingualText(text, languageCode))
}

func selectMultilingualText(text *packageinfo.MultilingualText, languageCode string) string {
	values := multilingualTextMap(text)
	if len(values) == 0 {
		return ""
	}

	available := make([]string, 0, len(values))
	for key := range values {
		if key != "_" {
			available = append(available, key)
		}
	}
	if matched, ok := internallanguage.BestMatch(languageCode, available); ok {
		return values[matched]
	}
	return values["_"]
}

func sortedMultilingualKeys(text *packageinfo.MultilingualText) []string {
	values := multilingualTextMap(text)
	keys := make([]string, 0, len(values))
	if _, ok := values["_"]; ok {
		keys = append(keys, "_")
	}
	extraKeys := make([]string, 0, len(values))
	for key := range values {
		if key != "_" {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	keys = append(keys, extraKeys...)
	return keys
}

func multilingualTextMap(text *packageinfo.MultilingualText) map[string]string {
	if text == nil {
		return nil
	}
	values := make(map[string]string, len(text.Texts)+1)
	values["_"] = text.Default
	for key, value := range text.Texts {
		if key == "_" {
			continue
		}
		values[key] = value
	}
	return values
}

func multilingualJSONValue(text *packageinfo.MultilingualText, languageCode string) any {
	if text == nil {
		return nil
	}
	if languageCode == "*" {
		return multilingualTextMap(text)
	}
	return selectMultilingualText(text, languageCode)
}

func importStatusText(status inspectImportStatus) string {
	switch {
	case !status.PackageInstalled:
		return "missingPackage"
	case !status.InferenceInstalled:
		return "missingInference"
	default:
		return "ready"
	}
}

func dependencyStatusText(status inspectDependencyStatus) string {
	if status.Installed {
		return "installed"
	}
	return "missingDependency"
}

func printSectionTitle(out io.Writer, title string) {
	fmt.Fprintln(out, inspectSectionStyle.Render(title))
}

func printSubsectionLabel(out io.Writer, indent string, label string) {
	fmt.Fprintf(out, "%s%s\n", indent, inspectKeyStyle.Render(label+":"))
}

func printField(out io.Writer, indent string, key string, value string) {
	fmt.Fprintf(out, "%s%s %s\n", indent, inspectKeyStyle.Render(key+":"), value)
}

func printEmpty(out io.Writer, indent string) {
	fmt.Fprintf(out, "%s%s\n", indent, inspectEmptyStyle.Render("(none)"))
}

func dependencyStatusLabel(status inspectDependencyStatus) string {
	if status.Installed {
		return inspectOKStyle.Render("✓ Installed")
	}
	return inspectWarningStyle.Render("? Missing dependency")
}

func importStatusLabel(status inspectImportStatus) string {
	switch {
	case !status.PackageInstalled:
		return inspectWarningStyle.Render("? Missing package")
	case !status.InferenceInstalled:
		return inspectErrorStyle.Render("✗ Missing inference")
	default:
		return inspectOKStyle.Render("✓ Ready")
	}
}

func splitTextLabel(label string) (string, string) {
	trimmed := strings.TrimLeft(label, " ")
	return label[:len(label)-len(trimmed)], trimmed
}

func writeInspectError(jsonOutput bool, out io.Writer, code string, message string, err error) error {
	if jsonOutput {
		encodeErr := json.NewEncoder(out).Encode(inspectOutput{
			OK:      false,
			Command: "inspect",
			Error: &inspectError{
				Code:    code,
				Message: message,
			},
		})
		if encodeErr != nil {
			return encodeErr
		}
	}
	return err
}
