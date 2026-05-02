package packagearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"diffscope-package-manager/packageinfo"

	"github.com/go-playground/validator/v10"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

const (
	DescriptionFormatAuto = ""
	DescriptionFormatJSON = "json"
	DescriptionFormatTOML = "toml"
	DescriptionFormatYAML = "yaml"
	DescriptionFormatYML  = "yml"
)

// PackOptions controls package planning.
type PackOptions struct {
	DescFormat string
	OutputFile string
}

// PackWarning is a non-fatal package authoring issue.
type PackWarning struct {
	Path    string
	Message string
}

// PackConversion records a description file that will be packed as JSON.
type PackConversion struct {
	SourcePath string
	PackedPath string
}

// PackProgress reports package creation progress.
type PackProgress struct {
	FilePath string
	Current  int64
	Total    int64
}

// PackPlan is the validated package creation plan.
type PackPlan struct {
	SourceDir           string
	OutputFile          string
	PackageID           string
	Version             packageinfo.PackageVersion
	DescriptionPath     string
	DescriptionFormat   string
	Files               []PackFile
	InferenceCount      int
	SingerCount         int
	DescriptionCount    int
	Warnings            []PackWarning
	Conversions         []PackConversion
	ConvertedFilesCount int
	TotalBytes          int64
}

// PackFile is one file to write into the package archive.
type PackFile struct {
	SourcePath string
	PackedPath string
	Data       []byte
	Size       int64
}

type packContext struct {
	sourceDir     string
	validator     *validator.Validate
	warnings      []PackWarning
	conversions   []PackConversion
	generated     map[string][]byte
	skippedSource map[string]struct{}
	inferenceIDs  map[string]struct{}
}

type descriptionFile struct {
	sourcePath string
	packedPath string
	format     string
	data       []byte
}

// PlanPackage validates a package directory and returns the files that would be packed.
func PlanPackage(sourceDir string, options PackOptions) (PackPlan, error) {
	if sourceDir == "" {
		return PackPlan{}, fmt.Errorf("pack package: source directory is required")
	}
	absoluteSourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return PackPlan{}, fmt.Errorf("pack package: resolve source directory: %w", err)
	}
	info, err := os.Stat(absoluteSourceDir)
	if err != nil {
		return PackPlan{}, fmt.Errorf("pack package: stat source directory: %w", err)
	}
	if !info.IsDir() {
		return PackPlan{}, fmt.Errorf("pack package: source is not a directory: %s", absoluteSourceDir)
	}

	validate := validator.New()
	if err := packageinfo.RegisterValidator(validate); err != nil {
		return PackPlan{}, fmt.Errorf("register package validators: %w", err)
	}
	ctx := &packContext{
		sourceDir:     absoluteSourceDir,
		validator:     validate,
		generated:     make(map[string][]byte),
		skippedSource: make(map[string]struct{}),
		inferenceIDs:  make(map[string]struct{}),
	}

	packageDescFile, packageDescription, err := readPackageDescriptionForPacking(ctx, options.DescFormat)
	if err != nil {
		return PackPlan{}, err
	}
	for _, candidate := range []string{"desc.json", "desc.toml", "desc.yml", "desc.yaml"} {
		addSkippedSource(ctx, candidate)
	}
	if err := validatePackageDescriptionForPacking(ctx, packageDescription); err != nil {
		return PackPlan{}, err
	}

	inferencePaths, err := readInferenceDescriptionsForPacking(ctx, packageDescription)
	if err != nil {
		return PackPlan{}, err
	}
	singerPaths, err := readSingerDescriptionsForPacking(ctx, packageDescription)
	if err != nil {
		return PackPlan{}, err
	}

	packageDescription.Contributes.Inferences = inferencePaths
	packageDescription.Contributes.Singers = singerPaths
	packageJSON, err := marshalPackedDescription(packageDescription)
	if err != nil {
		return PackPlan{}, fmt.Errorf("pack package: marshal desc.json: %w", err)
	}
	if err := addGeneratedFile(ctx, packageDescriptionPath, packageJSON); err != nil {
		return PackPlan{}, err
	}
	addSkippedSource(ctx, packageDescFile.sourcePath)
	addConversion(ctx, packageDescFile.sourcePath, packageDescriptionPath)

	files, total, err := buildPackFiles(ctx)
	if err != nil {
		return PackPlan{}, err
	}

	return PackPlan{
		SourceDir:           absoluteSourceDir,
		OutputFile:          options.OutputFile,
		PackageID:           packageDescription.ID,
		Version:             packageDescription.Version,
		DescriptionPath:     packageDescFile.sourcePath,
		DescriptionFormat:   packageDescFile.format,
		Files:               files,
		InferenceCount:      len(inferencePaths),
		SingerCount:         len(singerPaths),
		DescriptionCount:    1 + len(inferencePaths) + len(singerPaths),
		Warnings:            ctx.warnings,
		Conversions:         ctx.conversions,
		ConvertedFilesCount: len(ctx.conversions),
		TotalBytes:          total,
	}, nil
}

// CreatePackage writes a package archive from a plan.
func CreatePackage(ctx context.Context, plan PackPlan, progress func(PackProgress)) error {
	if plan.OutputFile == "" {
		return fmt.Errorf("pack package: output file is required")
	}
	if err := os.MkdirAll(filepath.Dir(plan.OutputFile), 0o755); err != nil {
		return fmt.Errorf("pack package: create output directory: %w", err)
	}

	file, err := os.OpenFile(plan.OutputFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("pack package: create output file: %w", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	var current int64
	for _, entry := range plan.Files {
		if err := ctx.Err(); err != nil {
			_ = writer.Close()
			return err
		}
		if progress != nil {
			progress(PackProgress{FilePath: entry.PackedPath, Current: atomic.LoadInt64(&current), Total: plan.TotalBytes})
		}
		if err := writePackFile(ctx, writer, entry, plan.TotalBytes, &current, progress); err != nil {
			_ = writer.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("pack package: close zip writer: %w", err)
	}
	return nil
}

func readPackageDescriptionForPacking(ctx *packContext, format string) (descriptionFile, packageinfo.PackageDescription, error) {
	desc, err := findPackageDescriptionFile(ctx.sourceDir, format)
	if err != nil {
		return descriptionFile{}, packageinfo.PackageDescription{}, err
	}
	var description packageinfo.PackageDescription
	if err := decodeDescription(desc.data, desc.format, &description); err != nil {
		return descriptionFile{}, packageinfo.PackageDescription{}, fmt.Errorf("parse %q: %w", desc.sourcePath, err)
	}
	if err := ctx.validator.Struct(description); err != nil {
		return descriptionFile{}, packageinfo.PackageDescription{}, fmt.Errorf("validate %q: %w", desc.sourcePath, err)
	}
	return desc, description, nil
}

func findPackageDescriptionFile(sourceDir string, requestedFormat string) (descriptionFile, error) {
	format, err := normalizeDescriptionFormat(requestedFormat)
	if err != nil {
		return descriptionFile{}, err
	}
	if format != DescriptionFormatAuto {
		name := "desc." + format
		if format == DescriptionFormatYML {
			name = "desc.yml"
		}
		return readDescriptionFileByPackagePath(sourceDir, name)
	}

	var found []descriptionFile
	for _, candidate := range []string{"desc.json", "desc.toml", "desc.yml", "desc.yaml"} {
		desc, err := readDescriptionFileByPackagePath(sourceDir, candidate)
		if err == nil {
			found = append(found, desc)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return descriptionFile{}, err
		}
	}
	if len(found) == 0 {
		return descriptionFile{}, fmt.Errorf("pack package: no package description file found")
	}
	if len(found) > 1 {
		paths := make([]string, 0, len(found))
		for _, desc := range found {
			paths = append(paths, desc.sourcePath)
		}
		return descriptionFile{}, fmt.Errorf("pack package: multiple package description files found: %s", strings.Join(paths, ", "))
	}
	return found[0], nil
}

func readDescriptionFileByPackagePath(sourceDir string, filePath string) (descriptionFile, error) {
	cleaned, err := cleanPackagePath(filePath)
	if err != nil {
		return descriptionFile{}, err
	}
	format, err := descriptionFormatFromPath(cleaned)
	if err != nil {
		return descriptionFile{}, err
	}
	absolutePath, err := sourceFilePath(sourceDir, cleaned)
	if err != nil {
		return descriptionFile{}, err
	}
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return descriptionFile{}, err
	}
	return descriptionFile{
		sourcePath: cleaned,
		packedPath: packedDescriptionPath(cleaned),
		format:     format,
		data:       data,
	}, nil
}

func readInferenceDescriptionsForPacking(ctx *packContext, packageDescription packageinfo.PackageDescription) ([]string, error) {
	packedPaths := make([]string, 0, len(packageDescription.Contributes.Inferences))
	seenSources := make(map[string]struct{}, len(packageDescription.Contributes.Inferences))
	seenPacked := make(map[string]string, len(packageDescription.Contributes.Inferences))
	for _, filePath := range packageDescription.Contributes.Inferences {
		desc, err := readContributedDescriptionFile(ctx.sourceDir, filePath)
		if err != nil {
			return nil, err
		}
		if err := rejectContributionDuplicate(seenSources, seenPacked, desc); err != nil {
			return nil, err
		}

		var description packageinfo.InferenceDescription
		if err := decodeDescription(desc.data, desc.format, &description); err != nil {
			return nil, fmt.Errorf("parse %q: %w", desc.sourcePath, err)
		}
		if err := ctx.validator.Struct(description); err != nil {
			return nil, fmt.Errorf("validate %q: %w", desc.sourcePath, err)
		}
		if _, ok := ctx.inferenceIDs[description.ID]; ok {
			return nil, fmt.Errorf("pack package: inference %q appears more than once", description.ID)
		}
		ctx.inferenceIDs[description.ID] = struct{}{}
		if description.Name == nil {
			addWarning(ctx, desc.sourcePath, "inference missing optional field: name")
		}

		data, err := marshalPackedDescription(description)
		if err != nil {
			return nil, fmt.Errorf("pack package: marshal %q: %w", desc.sourcePath, err)
		}
		if err := addGeneratedFile(ctx, desc.packedPath, data); err != nil {
			return nil, err
		}
		addSkippedSource(ctx, desc.sourcePath)
		addConversion(ctx, desc.sourcePath, desc.packedPath)
		packedPaths = append(packedPaths, desc.packedPath)
	}
	return packedPaths, nil
}

func readSingerDescriptionsForPacking(ctx *packContext, packageDescription packageinfo.PackageDescription) ([]string, error) {
	packedPaths := make([]string, 0, len(packageDescription.Contributes.Singers))
	seenSources := make(map[string]struct{}, len(packageDescription.Contributes.Singers))
	seenPacked := make(map[string]string, len(packageDescription.Contributes.Singers))
	seenSingerIDs := make(map[string]struct{}, len(packageDescription.Contributes.Singers))
	for _, filePath := range packageDescription.Contributes.Singers {
		desc, err := readContributedDescriptionFile(ctx.sourceDir, filePath)
		if err != nil {
			return nil, err
		}
		if err := rejectContributionDuplicate(seenSources, seenPacked, desc); err != nil {
			return nil, err
		}

		var description packageinfo.SingerDescription
		if err := decodeDescription(desc.data, desc.format, &description); err != nil {
			return nil, fmt.Errorf("parse %q: %w", desc.sourcePath, err)
		}
		if err := ctx.validator.Struct(description); err != nil {
			return nil, fmt.Errorf("validate %q: %w", desc.sourcePath, err)
		}
		if _, ok := seenSingerIDs[description.ID]; ok {
			return nil, fmt.Errorf("pack package: singer %q appears more than once", description.ID)
		}
		seenSingerIDs[description.ID] = struct{}{}
		if err := validateSingerDescriptionForPacking(ctx, packageDescription, desc.sourcePath, description); err != nil {
			return nil, err
		}

		data, err := marshalPackedDescription(description)
		if err != nil {
			return nil, fmt.Errorf("pack package: marshal %q: %w", desc.sourcePath, err)
		}
		if err := addGeneratedFile(ctx, desc.packedPath, data); err != nil {
			return nil, err
		}
		addSkippedSource(ctx, desc.sourcePath)
		addConversion(ctx, desc.sourcePath, desc.packedPath)
		packedPaths = append(packedPaths, desc.packedPath)
	}
	return packedPaths, nil
}

func readContributedDescriptionFile(sourceDir string, filePath string) (descriptionFile, error) {
	desc, err := readDescriptionFileByPackagePath(sourceDir, filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return descriptionFile{}, fmt.Errorf("pack package: contributed description %q not found", filePath)
		}
		return descriptionFile{}, err
	}
	return desc, nil
}

func rejectContributionDuplicate(seenSources map[string]struct{}, seenPacked map[string]string, desc descriptionFile) error {
	if _, ok := seenSources[desc.sourcePath]; ok {
		return fmt.Errorf("pack package: contributed description %q appears more than once", desc.sourcePath)
	}
	seenSources[desc.sourcePath] = struct{}{}
	if existing, ok := seenPacked[desc.packedPath]; ok {
		return fmt.Errorf("pack package: contributed descriptions %q and %q both pack as %q", existing, desc.sourcePath, desc.packedPath)
	}
	seenPacked[desc.packedPath] = desc.sourcePath
	return nil
}

func validatePackageDescriptionForPacking(ctx *packContext, description packageinfo.PackageDescription) error {
	if description.Name == nil {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: name")
	}
	if description.Description == nil {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: description")
	}
	if description.Vendor == nil {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: vendor")
	}
	if description.Readme == nil {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: readme")
	} else if err := validateMultilingualPackagePaths(ctx, packageDescriptionPath, "readme", description.Readme); err != nil {
		return err
	}
	if description.License == nil {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: license")
	} else if err := validateMultilingualPackagePaths(ctx, packageDescriptionPath, "license", description.License); err != nil {
		return err
	}
	if description.URL == "" {
		addWarning(ctx, packageDescriptionPath, "package missing optional field: url")
	}

	seen := make(map[string]struct{}, len(description.Dependencies))
	for _, dependency := range description.Dependencies {
		if dependency.ID == description.ID {
			return fmt.Errorf("pack package: package depends on itself: %s", dependency.ID)
		}
		key := dependency.ID + "@" + dependency.Version.String()
		if _, ok := seen[key]; ok {
			return fmt.Errorf("pack package: dependency %q appears more than once", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateSingerDescriptionForPacking(
	ctx *packContext,
	packageDescription packageinfo.PackageDescription,
	filePath string,
	description packageinfo.SingerDescription,
) error {
	if description.Name == nil {
		addWarning(ctx, filePath, "singer missing optional field: name")
	}
	if description.Avatar == nil {
		addWarning(ctx, filePath, "singer missing optional field: avatar")
	} else if err := validateSingerImagePaths(ctx, filePath, "avatar", description.Avatar, true); err != nil {
		return err
	}
	if description.Background == nil {
		addWarning(ctx, filePath, "singer missing optional field: background")
	} else if err := validateSingerImagePaths(ctx, filePath, "background", description.Background, false); err != nil {
		return err
	}
	if len(description.DemoAudio) == 0 {
		addWarning(ctx, filePath, "singer missing optional field: demoAudio")
	}
	for index, demo := range description.DemoAudio {
		if err := validateDemoAudioPaths(ctx, filePath, fmt.Sprintf("demoAudio[%d].path", index), &demo.Path); err != nil {
			return err
		}
	}

	for index, item := range description.Imports {
		importPackageID := item.ID
		if importPackageID == "" {
			importPackageID = packageDescription.ID
		}
		if importPackageID != packageDescription.ID {
			continue
		}
		if item.Version != nil && item.Version.Compare(packageDescription.Version) != 0 {
			continue
		}
		if _, ok := ctx.inferenceIDs[item.InferenceID]; !ok {
			return fmt.Errorf("pack package: singer import %d in %q references missing current-package inference %q", index, filePath, item.InferenceID)
		}
	}
	return nil
}

func validateMultilingualPackagePaths(ctx *packContext, ownerPath string, field string, text *packageinfo.MultilingualText) error {
	values := multilingualPathValues(text)
	for key, value := range values {
		if _, err := existingSourceFilePath(ctx.sourceDir, value); err != nil {
			return fmt.Errorf("pack package: %s[%s] in %q: %w", field, key, ownerPath, err)
		}
	}
	return nil
}

func validateSingerImagePaths(ctx *packContext, ownerPath string, field string, text *packageinfo.MultilingualText, requireSquare bool) error {
	values := multilingualPathValues(text)
	for key, value := range values {
		filePath, err := existingSourceFilePath(ctx.sourceDir, value)
		if err != nil {
			return fmt.Errorf("pack package: %s[%s] in %q: %w", field, key, ownerPath, err)
		}
		width, height, err := pngDimensions(filePath)
		if err != nil {
			addWarning(ctx, ownerPath, fmt.Sprintf("%s[%s] is not a PNG file (may not be supported by the editor): %s", field, key, value))
			continue
		}
		if requireSquare && width != height {
			addWarning(ctx, ownerPath, fmt.Sprintf("%s[%s] is not square (may not display correctly): %s", field, key, value))
		}
	}
	return nil
}

func validateDemoAudioPaths(ctx *packContext, ownerPath string, field string, text *packageinfo.MultilingualText) error {
	values := multilingualPathValues(text)
	for key, value := range values {
		filePath, err := existingSourceFilePath(ctx.sourceDir, value)
		if err != nil {
			return fmt.Errorf("pack package: %s[%s] in %q: %w", field, key, ownerPath, err)
		}
		ok, err := isOggVorbis(filePath)
		if err != nil || !ok {
			addWarning(ctx, ownerPath, fmt.Sprintf("%s[%s] is not OGG Vorbis (may not be supported by the editor): %s", field, key, value))
		}
	}
	return nil
}

func decodeDescription(data []byte, format string, out any) error {
	jsonData, err := descriptionJSON(data, format)
	if err != nil {
		return err
	}
	return json.Unmarshal(jsonData, out)
}

func descriptionJSON(data []byte, format string) ([]byte, error) {
	switch format {
	case DescriptionFormatJSON:
		return data, nil
	case DescriptionFormatTOML:
		var value any
		if err := toml.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		return json.Marshal(normalizeJSONValue(value))
	case DescriptionFormatYAML, DescriptionFormatYML:
		var value any
		if err := yaml.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		return json.Marshal(normalizeJSONValue(value))
	default:
		return nil, fmt.Errorf("unsupported description format %q", format)
	}
}

func normalizeJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, value := range typed {
			normalized[key] = normalizeJSONValue(value)
		}
		return normalized
	case map[any]any:
		normalized := make(map[string]any, len(typed))
		for key, value := range typed {
			normalized[fmt.Sprint(key)] = normalizeJSONValue(value)
		}
		return normalized
	case []any:
		normalized := make([]any, 0, len(typed))
		for _, value := range typed {
			normalized = append(normalized, normalizeJSONValue(value))
		}
		return normalized
	default:
		return value
	}
}

func marshalPackedDescription(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func buildPackFiles(ctx *packContext) ([]PackFile, int64, error) {
	files := make(map[string]PackFile, len(ctx.generated))
	for packedPath, data := range ctx.generated {
		files[packedPath] = PackFile{
			PackedPath: packedPath,
			Data:       data,
			Size:       int64(len(data)),
		}
	}

	err := filepath.WalkDir(ctx.sourceDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == ctx.sourceDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("pack package: source file %q is a symbolic link", filePath)
		}
		if entry.IsDir() {
			return nil
		}
		packagePath, err := packagePathFromSource(ctx.sourceDir, filePath)
		if err != nil {
			return err
		}
		if _, ok := ctx.skippedSource[packagePath]; ok {
			return nil
		}
		if _, ok := files[packagePath]; ok {
			return fmt.Errorf("pack package: generated description conflicts with source file %q", packagePath)
		}
		files[packagePath] = PackFile{
			SourcePath: filePath,
			PackedPath: packagePath,
			Size:       info.Size(),
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	result := make([]PackFile, 0, len(files))
	var total int64
	for _, file := range files {
		result = append(result, file)
		total += file.Size
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].PackedPath < result[j].PackedPath
	})
	return result, total, nil
}

func writePackFile(ctx context.Context, writer *zip.Writer, entry PackFile, total int64, current *int64, progress func(PackProgress)) error {
	header := &zip.FileHeader{
		Name:   entry.PackedPath,
		Method: zip.Deflate,
	}
	header.SetMode(0o644)
	dst, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("pack package: create zip entry %q: %w", entry.PackedPath, err)
	}

	if entry.Data != nil {
		if _, err := dst.Write(entry.Data); err != nil {
			return fmt.Errorf("pack package: write zip entry %q: %w", entry.PackedPath, err)
		}
		value := atomic.AddInt64(current, int64(len(entry.Data)))
		if progress != nil {
			progress(PackProgress{FilePath: entry.PackedPath, Current: value, Total: total})
		}
		return nil
	}

	src, err := os.Open(entry.SourcePath)
	if err != nil {
		return fmt.Errorf("pack package: open source file %q: %w", entry.SourcePath, err)
	}
	defer src.Close()

	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, err := dst.Write(buffer[:n]); err != nil {
				return fmt.Errorf("pack package: write zip entry %q: %w", entry.PackedPath, err)
			}
			value := atomic.AddInt64(current, int64(n))
			if progress != nil {
				progress(PackProgress{FilePath: entry.PackedPath, Current: value, Total: total})
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("pack package: read source file %q: %w", entry.SourcePath, readErr)
		}
	}
}

func normalizeDescriptionFormat(format string) (string, error) {
	switch strings.ToLower(format) {
	case "":
		return DescriptionFormatAuto, nil
	case DescriptionFormatJSON:
		return DescriptionFormatJSON, nil
	case DescriptionFormatTOML:
		return DescriptionFormatTOML, nil
	case DescriptionFormatYAML:
		return DescriptionFormatYAML, nil
	case DescriptionFormatYML:
		return DescriptionFormatYML, nil
	default:
		return "", fmt.Errorf("pack package: unsupported description format %q", format)
	}
}

func descriptionFormatFromPath(filePath string) (string, error) {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".json":
		return DescriptionFormatJSON, nil
	case ".toml":
		return DescriptionFormatTOML, nil
	case ".yaml":
		return DescriptionFormatYAML, nil
	case ".yml":
		return DescriptionFormatYML, nil
	default:
		return "", fmt.Errorf("pack package: unsupported description file format: %s", filePath)
	}
}

func packedDescriptionPath(filePath string) string {
	return strings.TrimSuffix(filePath, path.Ext(filePath)) + ".json"
}

func addConversion(ctx *packContext, sourcePath string, packedPath string) {
	if sourcePath == packedPath {
		return
	}
	ctx.conversions = append(ctx.conversions, PackConversion{SourcePath: sourcePath, PackedPath: packedPath})
}

func addGeneratedFile(ctx *packContext, packedPath string, data []byte) error {
	if _, ok := ctx.generated[packedPath]; ok {
		return fmt.Errorf("pack package: generated description %q appears more than once", packedPath)
	}
	ctx.generated[packedPath] = data
	return nil
}

func addWarning(ctx *packContext, filePath string, message string) {
	ctx.warnings = append(ctx.warnings, PackWarning{Path: filePath, Message: message})
}

func addSkippedSource(ctx *packContext, packagePath string) {
	ctx.skippedSource[packagePath] = struct{}{}
}

func multilingualPathValues(text *packageinfo.MultilingualText) map[string]string {
	values := map[string]string{"_": text.Default}
	for key, value := range text.Texts {
		values[key] = value
	}
	return values
}

func existingSourceFilePath(sourceDir string, filePath string) (string, error) {
	target, err := sourceFilePath(sourceDir, filePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file %q not found", filePath)
		}
		return "", fmt.Errorf("stat file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("file %q is a directory", filePath)
	}
	return target, nil
}

func sourceFilePath(sourceDir string, filePath string) (string, error) {
	cleaned, err := cleanPackagePath(filePath)
	if err != nil {
		return "", err
	}
	target := filepath.Join(sourceDir, filepath.FromSlash(cleaned))
	cleanSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", fmt.Errorf("resolve package source directory: %w", err)
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve package source path %q: %w", filePath, err)
	}
	if cleanTarget != cleanSource && !isPathWithin(cleanTarget, cleanSource) {
		return "", fmt.Errorf("file %q escapes package root", filePath)
	}
	return cleanTarget, nil
}

func packagePathFromSource(sourceDir string, filePath string) (string, error) {
	relative, err := filepath.Rel(sourceDir, filePath)
	if err != nil {
		return "", err
	}
	packagePath := filepath.ToSlash(relative)
	return cleanPackagePath(packagePath)
}

func pngDimensions(filePath string) (int, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, err := png.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

func isOggVorbis(filePath string) (bool, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false, err
	}
	if len(data) < 4 || !bytes.Equal(data[:4], []byte("OggS")) {
		return false, nil
	}
	limit := len(data)
	if limit > 64*1024 {
		limit = 64 * 1024
	}
	return bytes.Contains(data[:limit], []byte{0x01, 'v', 'o', 'r', 'b', 'i', 's'}), nil
}
