package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"diffscope-package-manager/packagearchive"
	"diffscope-package-manager/packagedatabase"
	"diffscope-package-manager/packagedatabase/model"
	"diffscope-package-manager/packageinfo"

	"github.com/charmbracelet/lipgloss"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type installAction string

const (
	installActionInstall   installAction = "install"
	installActionOverwrite installAction = "overwrite"
	installActionSkip      installAction = "skip"
)

type installPackage struct {
	Path       string
	Reader     packageFileReader
	Inspection packagearchive.PackageInspection
	Hash       string
	Action     installAction
	Existing   *model.Package
}

type installEvent struct {
	Command string        `json:"command"`
	Event   string        `json:"event"`
	Data    any           `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type installReporter interface {
	InspectStart(paths []string)
	InspectOK(index int, pkg installPackage)
	InspectFail(index int, path string, err error)
	Notice(message string)
	DryRun(pkgs []installPackage)
	ExtractStart(pkgs []installPackage)
	ExtractProgress(index int, progress packagearchive.ExtractProgress)
	ExtractDone(index int, pkg installPackage)
	Result(pkg installPackage)
	Summary(installed int, overwritten int, skipped int)
	Error(code string, message string, details map[string]any, err error) error
}

var (
	installOKStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	installWarnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	installErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	installMutedStyle   = lipgloss.NewStyle().Faint(true)
	installPackageStyle = lipgloss.NewStyle().Bold(true)
)

// NewInstallCmd creates the install command.
func NewInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <packageFile>...",
		Short: "Install one or more packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			overwriteExisting, err := cmd.Flags().GetBool("overwrite-existing")
			if err != nil {
				return err
			}
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			return InstallPackages(
				args,
				viper.GetString("packages_dir"),
				overwriteExisting,
				dryRun,
				viper.GetBool("json"),
				cmd.OutOrStdout(),
			)
		},
	}

	cmd.Flags().Bool("overwrite-existing", false, "overwrite if a different package is already installed")
	cmd.Flags().Bool("dry-run", false, "check and report packages without installing them")
	return cmd
}

// InstallPackages is the command entrypoint for installing package archives.
func InstallPackages(packageFiles []string, packagesDir string, overwriteExisting bool, dryRun bool, jsonOutput bool, out io.Writer) error {
	reporter := newInstallReporter(jsonOutput, out)
	absolutePaths, err := absolutePackagePaths(packageFiles)
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	if err := rejectDuplicatePackageFiles(absolutePaths); err != nil {
		return reporter.Error("SCHEMA_ERROR", err.Error(), nil, err)
	}

	reporter.InspectStart(absolutePaths)
	packages, err := inspectInstallPackages(absolutePaths, reporter)
	if err != nil {
		closeInstallReaders(packages)
		return err
	}
	defer closeInstallReaders(packages)

	if err := rejectDuplicatePackageIdentities(packages); err != nil {
		return reporter.Error("ALREADY_INSTALLED", err.Error(), nil, err)
	}

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	defer sqlDB.Close()

	if err := classifyInstallActions(db, packages, overwriteExisting, reporter); err != nil {
		return err
	}
	if err := validateInstallDependencies(db, packages); err != nil {
		return reporter.Error(errorCodeForInstallError(err), err.Error(), nil, err)
	}
	if err := validateInstallImports(db, packages); err != nil {
		return reporter.Error("NOT_FOUND", err.Error(), nil, err)
	}

	if dryRun {
		reporter.DryRun(packages)
		return nil
	}

	if err := ensureInstallDirectoriesAvailable(packagesDir, packages); err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}

	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("BEGIN EXCLUSIVE").Error; err != nil {
		return reporter.Error("IO_ERROR", fmt.Sprintf("begin exclusive transaction: %v", err), nil, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = db.Exec("ROLLBACK").Error
		}
	}()

	createdDirs, err := extractInstallPackages(context.Background(), packagesDir, packages, reporter)
	if err != nil {
		rollbackInstallDirs(createdDirs)
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}

	txDB := db.Session(&gorm.Session{SkipDefaultTransaction: true})
	if err := writeInstalledPackages(txDB, packages); err != nil {
		rollbackInstallDirs(createdDirs)
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}

	promotedDirs, err := promoteOverwrittenPackageDirs(packagesDir, packages)
	if err != nil {
		rollbackPromotedPackageDirs(promotedDirs)
		rollbackInstallDirs(createdDirs)
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}

	if err := db.Exec("COMMIT").Error; err != nil {
		rollbackPromotedPackageDirs(promotedDirs)
		rollbackInstallDirs(createdDirs)
		return reporter.Error("IO_ERROR", fmt.Sprintf("commit install transaction: %v", err), nil, err)
	}
	committed = true

	cleanupPromotedPackageDirBackups(promotedDirs)
	installed, overwritten, skipped := 0, 0, 0
	for _, pkg := range packages {
		switch pkg.Action {
		case installActionInstall:
			installed++
			reporter.Result(pkg)
		case installActionOverwrite:
			overwritten++
			reporter.Result(pkg)
		case installActionSkip:
			skipped++
			reporter.Result(pkg)
		}
	}
	reporter.Summary(installed, overwritten, skipped)
	return nil
}

func absolutePackagePaths(packageFiles []string) ([]string, error) {
	paths := make([]string, 0, len(packageFiles))
	for _, packageFile := range packageFiles {
		absolute, err := filepath.Abs(packageFile)
		if err != nil {
			return nil, fmt.Errorf("resolve package file path %q: %w", packageFile, err)
		}
		paths = append(paths, absolute)
	}
	return paths, nil
}

func rejectDuplicatePackageFiles(paths []string) error {
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		key := strings.ToLower(filepath.Clean(path))
		if _, ok := seen[key]; ok {
			return fmt.Errorf("package file %q was specified more than once", path)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func inspectInstallPackages(paths []string, reporter installReporter) ([]installPackage, error) {
	packages := make([]installPackage, len(paths))
	errs := make(chan installInspectResult, len(paths))
	var wg sync.WaitGroup
	for index, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pkg, err := inspectInstallPackage(path)
			errs <- installInspectResult{index: index, pkg: pkg, err: err}
		}()
	}
	wg.Wait()
	close(errs)

	var firstErr error
	var firstIndex int
	for result := range errs {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
			firstIndex = result.index
		}
		packages[result.index] = result.pkg
	}

	for index, pkg := range packages {
		if firstErr != nil && index == firstIndex {
			reporter.InspectFail(index, paths[index], firstErr)
			return packages, reporter.Error("SCHEMA_ERROR", firstErr.Error(), map[string]any{"packageFile": paths[index]}, firstErr)
		}
		reporter.InspectOK(index, pkg)
	}
	return packages, nil
}

type installInspectResult struct {
	index int
	pkg   installPackage
	err   error
}

func inspectInstallPackage(path string) (installPackage, error) {
	reader, err := openPackageFileReader(path)
	if err != nil {
		return installPackage{}, err
	}
	inspection, err := packagearchive.InspectPackage(reader)
	if err != nil {
		reader.Close()
		return installPackage{}, err
	}
	hash, err := packagearchive.ComputePackageHash(reader)
	if err != nil {
		reader.Close()
		return installPackage{}, err
	}
	return installPackage{
		Path:       path,
		Reader:     reader,
		Inspection: inspection,
		Hash:       fmt.Sprintf("%x", hash),
		Action:     installActionInstall,
	}, nil
}

func closeInstallReaders(packages []installPackage) {
	for _, pkg := range packages {
		if pkg.Reader.File != nil {
			_ = pkg.Reader.Close()
		}
	}
}

func rejectDuplicatePackageIdentities(packages []installPackage) error {
	seen := make(map[string]string, len(packages))
	for _, pkg := range packages {
		key := installPackageKey(pkg.Inspection.ID, pkg.Inspection.Version.String())
		if previous, ok := seen[key]; ok {
			return fmt.Errorf("package %s@%s appears more than once in this install command: %s and %s",
				pkg.Inspection.ID, pkg.Inspection.Version.String(), previous, pkg.Path)
		}
		seen[key] = pkg.Path
	}
	return nil
}

func classifyInstallActions(db *gorm.DB, packages []installPackage, overwriteExisting bool, reporter installReporter) error {
	for index := range packages {
		pkg := &packages[index]
		var existing []model.Package
		err := db.Where("id = ? AND version = ?", pkg.Inspection.ID, pkg.Inspection.Version.String()).Limit(1).Find(&existing).Error
		if err != nil {
			return reporter.Error("IO_ERROR", err.Error(), nil, err)
		}
		if len(existing) == 0 {
			pkg.Action = installActionInstall
			continue
		}
		pkg.Existing = &existing[0]
		if existing[0].Hash == pkg.Hash {
			pkg.Action = installActionSkip
			reporter.Notice(fmt.Sprintf("%s is already installed with the same hash; skipping", installPackageRef(*pkg)))
			continue
		}
		if !overwriteExisting {
			err := fmt.Errorf("%s is already installed with a different hash; use --overwrite-existing to replace it", installPackageRef(*pkg))
			return reporter.Error("ALREADY_INSTALLED", err.Error(), nil, err)
		}
		pkg.Action = installActionOverwrite
		reporter.Notice(fmt.Sprintf("%s is already installed with a different hash; will overwrite", installPackageRef(*pkg)))
	}
	return nil
}

func validateInstallDependencies(db *gorm.DB, packages []installPackage) error {
	available, err := installedPackageSet(db)
	if err != nil {
		return err
	}
	graph, err := installedDependencyGraph(db)
	if err != nil {
		return err
	}
	for _, pkg := range packages {
		key := installPackageKey(pkg.Inspection.ID, pkg.Inspection.Version.String())
		available[key] = struct{}{}
		if pkg.Action != installActionSkip {
			graph[key] = graph[key][:0]
			for _, dependency := range pkg.Inspection.Dependencies {
				depKey := installPackageKey(dependency.PackageID, dependency.Version.String())
				graph[key] = append(graph[key], depKey)
			}
		}
	}
	for _, pkg := range packages {
		for _, dependency := range pkg.Inspection.Dependencies {
			depKey := installPackageKey(dependency.PackageID, dependency.Version.String())
			if _, ok := available[depKey]; !ok {
				return installDependencyMissingError{Package: installPackageRef(pkg), Dependency: dependency.String()}
			}
		}
	}
	if cycle := findDependencyCycle(graph); len(cycle) > 0 {
		return installDependencyCycleError{Cycle: cycle}
	}
	return nil
}

func validateInstallImports(db *gorm.DB, packages []installPackage) error {
	planned := make(map[string]map[string]struct{}, len(packages))
	for _, pkg := range packages {
		key := installPackageKey(pkg.Inspection.ID, pkg.Inspection.Version.String())
		inferences := make(map[string]struct{}, len(pkg.Inspection.Contributes.Inferences))
		for _, inference := range pkg.Inspection.Contributes.Inferences {
			inferences[inference.ID] = struct{}{}
		}
		planned[key] = inferences
	}

	for _, pkg := range packages {
		for _, singer := range pkg.Inspection.Contributes.Singers {
			for _, imported := range singer.Imports {
				key := installPackageKey(imported.PackageID, imported.Version.String())
				if inferences, ok := planned[key]; ok {
					if _, ok := inferences[imported.InferenceID]; ok {
						continue
					}
					return fmt.Errorf("%s imports missing inference %s", installSingerRef(pkg, singer.ID), imported.String())
				}
				ok, err := inferenceInstalled(db, imported.PackageID, imported.Version.String(), imported.InferenceID)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("%s imports missing inference %s", installSingerRef(pkg, singer.ID), imported.String())
				}
			}
		}
	}
	return nil
}

func installedPackageSet(db *gorm.DB) (map[string]struct{}, error) {
	var rows []model.Package
	if err := db.Find(&rows).Error; err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		set[installPackageKey(row.ID, row.Version)] = struct{}{}
	}
	return set, nil
}

func installedDependencyGraph(db *gorm.DB) (map[string][]string, error) {
	var packages []model.Package
	if err := db.Find(&packages).Error; err != nil {
		return nil, err
	}
	graph := make(map[string][]string, len(packages))
	for _, pkg := range packages {
		graph[installPackageKey(pkg.ID, pkg.Version)] = nil
	}
	var dependencies []model.Dependency
	if err := db.Find(&dependencies).Error; err != nil {
		return nil, err
	}
	for _, dependency := range dependencies {
		key := installPackageKey(dependency.PackageID, dependency.PackageVersion)
		graph[key] = append(graph[key], installPackageKey(dependency.DependentPackageID, dependency.DependentPackageVersion))
	}
	return graph, nil
}

func findDependencyCycle(graph map[string][]string) []string {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)
	state := make(map[string]int, len(graph))
	stack := make([]string, 0, len(graph))
	var visit func(string) []string
	visit = func(node string) []string {
		state[node] = visiting
		stack = append(stack, node)
		for _, next := range graph[node] {
			switch state[next] {
			case visiting:
				for index, value := range stack {
					if value == next {
						return append(append([]string(nil), stack[index:]...), next)
					}
				}
			case unvisited:
				if cycle := visit(next); len(cycle) > 0 {
					return cycle
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[node] = visited
		return nil
	}
	nodes := make([]string, 0, len(graph))
	for node := range graph {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		if state[node] == unvisited {
			if cycle := visit(node); len(cycle) > 0 {
				return cycle
			}
		}
	}
	return nil
}

func ensureInstallDirectoriesAvailable(packagesDir string, packages []installPackage) error {
	for _, pkg := range packages {
		if pkg.Action == installActionSkip {
			continue
		}
		destination := installedPackageDir(packagesDir, pkg.Inspection.ID, pkg.Inspection.Version.String())
		if _, err := os.Stat(destination); err == nil {
			if pkg.Action == installActionOverwrite {
				staging := stagedInstallPackageDir(packagesDir, pkg)
				if _, err := os.Stat(staging); err == nil {
					return fmt.Errorf("package staging directory already exists: %s", staging)
				} else if !os.IsNotExist(err) {
					return fmt.Errorf("stat package staging directory %s: %w", staging, err)
				}
				continue
			}
			return fmt.Errorf("package directory already exists: %s", destination)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat package directory %s: %w", destination, err)
		}
	}
	return nil
}

func extractInstallPackages(ctx context.Context, packagesDir string, packages []installPackage, reporter installReporter) ([]string, error) {
	reporter.ExtractStart(packages)
	createdDirs := make([]string, 0, len(packages))
	var mu sync.Mutex
	errs := make(chan error, len(packages))
	var wg sync.WaitGroup
	for index, pkg := range packages {
		if pkg.Action == installActionSkip {
			continue
		}
		destination := installExtractDestination(packagesDir, pkg)
		mu.Lock()
		createdDirs = append(createdDirs, destination)
		mu.Unlock()
		wg.Add(1)
		go func(index int, pkg installPackage, destination string) {
			defer wg.Done()
			err := packagearchive.ExtractPackage(ctx, pkg.Reader, destination, func(progress packagearchive.ExtractProgress) {
				reporter.ExtractProgress(index, progress)
			})
			if err != nil {
				errs <- err
				return
			}
			reporter.ExtractDone(index, pkg)
		}(index, pkg, destination)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return createdDirs, err
	}
	return createdDirs, nil
}

func installExtractDestination(packagesDir string, pkg installPackage) string {
	if pkg.Action == installActionOverwrite {
		return stagedInstallPackageDir(packagesDir, pkg)
	}
	return installedPackageDir(packagesDir, pkg.Inspection.ID, pkg.Inspection.Version.String())
}

func stagedInstallPackageDir(packagesDir string, pkg installPackage) string {
	hashPrefix := pkg.Hash
	if len(hashPrefix) > 16 {
		hashPrefix = hashPrefix[:16]
	}
	return filepath.Join(packagesDir, "."+installedPackageDirName(pkg.Inspection.ID, pkg.Inspection.Version.String())+".new-"+hashPrefix)
}

func writeInstalledPackages(db *gorm.DB, packages []installPackage) error {
	now := time.Now().UnixMilli()
	for _, pkg := range packages {
		if pkg.Action == installActionSkip {
			continue
		}
		if pkg.Action == installActionOverwrite {
			if err := clearOwnedPackageRows(db, pkg.Inspection.ID, pkg.Inspection.Version.String()); err != nil {
				return err
			}
		}
		url := optionalString(pkg.Inspection.URL)
		if err := db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}, {Name: "version"}},
			DoUpdates: clause.AssignmentColumns([]string{"hash", "installed_at", "url"}),
		}).Create(&model.Package{
			ID:          pkg.Inspection.ID,
			Version:     pkg.Inspection.Version.String(),
			Hash:        pkg.Hash,
			InstalledAt: now,
			URL:         url,
		}).Error; err != nil {
			return err
		}
		if err := createPackageRows(db, pkg); err != nil {
			return err
		}
	}
	return nil
}

func clearOwnedPackageRows(db *gorm.DB, packageID string, version string) error {
	deletes := []struct {
		model any
		where string
		args  []any
	}{
		{&model.SingerImport{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.SingerDemoAudioMultilingualInfo{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.SingerDemoAudio{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.SingerMultilingualInfo{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.Singer{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.InferenceMultilingualInfo{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.Inference{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.Dependency{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
		{&model.PackageMultilingualInfo{}, "package_id = ? AND package_version = ?", []any{packageID, version}},
	}
	for _, item := range deletes {
		if err := db.Where(item.where, item.args...).Delete(item.model).Error; err != nil {
			return err
		}
	}
	return nil
}

func createPackageRows(db *gorm.DB, pkg installPackage) error {
	packageID := pkg.Inspection.ID
	version := pkg.Inspection.Version.String()
	for _, row := range packageTextRows(packageID, version, pkg.Inspection) {
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	for _, dependency := range pkg.Inspection.Dependencies {
		if err := db.Create(&model.Dependency{
			PackageID:               packageID,
			PackageVersion:          version,
			DependentPackageID:      dependency.PackageID,
			DependentPackageVersion: dependency.Version.String(),
		}).Error; err != nil {
			return err
		}
	}
	for _, inference := range pkg.Inspection.Contributes.Inferences {
		if err := db.Create(&model.Inference{ID: inference.ID, PackageID: packageID, PackageVersion: version}).Error; err != nil {
			return err
		}
		for _, row := range inferenceTextRows(packageID, version, inference) {
			if err := db.Create(&row).Error; err != nil {
				return err
			}
		}
	}
	for _, singer := range pkg.Inspection.Contributes.Singers {
		if err := db.Create(&model.Singer{ID: singer.ID, PackageID: packageID, PackageVersion: version, Class: singer.Class}).Error; err != nil {
			return err
		}
		for _, row := range singerTextRows(packageID, version, singer) {
			if err := db.Create(&row).Error; err != nil {
				return err
			}
		}
		for _, imported := range singer.Imports {
			if err := db.Create(&model.SingerImport{
				SingerID:               singer.ID,
				PackageID:              packageID,
				PackageVersion:         version,
				ImportedInferenceID:    imported.InferenceID,
				ImportedPackageID:      imported.PackageID,
				ImportedPackageVersion: imported.Version.String(),
			}).Error; err != nil {
				return err
			}
		}
		for index, demo := range singer.DemoAudio {
			if err := db.Create(&model.SingerDemoAudio{
				Index:          index,
				SingerID:       singer.ID,
				PackageID:      packageID,
				PackageVersion: version,
			}).Error; err != nil {
				return err
			}
			for _, row := range demoAudioTextRows(packageID, version, singer.ID, index, demo) {
				if err := db.Create(&row).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func packageTextRows(packageID string, version string, inspection packagearchive.PackageInspection) []model.PackageMultilingualInfo {
	rows := map[string]*model.PackageMultilingualInfo{}
	addPackageText(rows, packageID, version, "Name", inspection.Name)
	addPackageText(rows, packageID, version, "Description", inspection.Description)
	addPackageText(rows, packageID, version, "Vendor", inspection.Vendor)
	addPackageText(rows, packageID, version, "Readme", inspection.Readme)
	addPackageText(rows, packageID, version, "License", inspection.License)
	result := make([]model.PackageMultilingualInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Language < result[j].Language })
	return result
}

func addPackageText(rows map[string]*model.PackageMultilingualInfo, packageID string, version string, field string, text *packageinfo.MultilingualText) {
	for language, value := range multilingualInstallValues(text) {
		row := rows[language]
		if row == nil {
			row = &model.PackageMultilingualInfo{PackageID: packageID, PackageVersion: version, Language: language}
			rows[language] = row
		}
		setPackageTextField(row, field, value)
	}
}

func setPackageTextField(row *model.PackageMultilingualInfo, field string, value string) {
	valuePtr := installStringPointer(value)
	switch field {
	case "Name":
		row.Name = valuePtr
	case "Description":
		row.Description = valuePtr
	case "Vendor":
		row.Vendor = valuePtr
	case "Readme":
		row.Readme = valuePtr
	case "License":
		row.License = valuePtr
	}
}

func inferenceTextRows(packageID string, version string, inference packagearchive.InferenceInspection) []model.InferenceMultilingualInfo {
	values := multilingualInstallValues(inference.Name)
	rows := make([]model.InferenceMultilingualInfo, 0, len(values))
	for language, value := range values {
		rows = append(rows, model.InferenceMultilingualInfo{
			InferenceID:    inference.ID,
			PackageID:      packageID,
			PackageVersion: version,
			Language:       language,
			Name:           installStringPointer(value),
		})
	}
	return rows
}

func singerTextRows(packageID string, version string, singer packagearchive.SingerInspection) []model.SingerMultilingualInfo {
	rows := map[string]*model.SingerMultilingualInfo{}
	addSingerText(rows, packageID, version, singer.ID, "Name", singer.Name)
	addSingerText(rows, packageID, version, singer.ID, "Avatar", singer.Avatar)
	addSingerText(rows, packageID, version, singer.ID, "Background", singer.Background)
	result := make([]model.SingerMultilingualInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Language < result[j].Language })
	return result
}

func addSingerText(rows map[string]*model.SingerMultilingualInfo, packageID string, version string, singerID string, field string, text *packageinfo.MultilingualText) {
	for language, value := range multilingualInstallValues(text) {
		row := rows[language]
		if row == nil {
			row = &model.SingerMultilingualInfo{SingerID: singerID, PackageID: packageID, PackageVersion: version, Language: language}
			rows[language] = row
		}
		valuePtr := installStringPointer(value)
		switch field {
		case "Name":
			row.Name = valuePtr
		case "Avatar":
			row.Avatar = valuePtr
		case "Background":
			row.Background = valuePtr
		}
	}
}

func demoAudioTextRows(packageID string, version string, singerID string, index int, demo packagearchive.SingerDemoAudioInspection) []model.SingerDemoAudioMultilingualInfo {
	rows := map[string]*model.SingerDemoAudioMultilingualInfo{}
	addDemoAudioText(rows, packageID, version, singerID, index, "Name", &demo.Name)
	addDemoAudioText(rows, packageID, version, singerID, index, "Audio", &demo.Audio)
	result := make([]model.SingerDemoAudioMultilingualInfo, 0, len(rows))
	for _, row := range rows {
		result = append(result, *row)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Language < result[j].Language })
	return result
}

func addDemoAudioText(rows map[string]*model.SingerDemoAudioMultilingualInfo, packageID string, version string, singerID string, index int, field string, text *packageinfo.MultilingualText) {
	for language, value := range multilingualInstallValues(text) {
		row := rows[language]
		if row == nil {
			row = &model.SingerDemoAudioMultilingualInfo{DemoIndex: index, SingerID: singerID, PackageID: packageID, PackageVersion: version, Language: language}
			rows[language] = row
		}
		valuePtr := installStringPointer(value)
		if field == "Name" {
			row.Name = valuePtr
		} else {
			row.Audio = valuePtr
		}
	}
}

func multilingualInstallValues(text *packageinfo.MultilingualText) map[string]string {
	if text == nil {
		return nil
	}
	values := make(map[string]string, len(text.Texts)+1)
	values["_"] = text.Default
	for language, value := range text.Texts {
		values[language] = value
	}
	return values
}

func rollbackInstallDirs(dirs []string) {
	for _, dir := range dirs {
		_ = os.RemoveAll(dir)
	}
}

type promotedPackageDir struct {
	Final  string
	Backup string
}

func promoteOverwrittenPackageDirs(packagesDir string, packages []installPackage) ([]promotedPackageDir, error) {
	promoted := make([]promotedPackageDir, 0, len(packages))
	for _, pkg := range packages {
		if pkg.Action != installActionOverwrite || pkg.Existing == nil || pkg.Existing.Hash == "" || pkg.Existing.Hash == pkg.Hash {
			continue
		}
		final := installedPackageDir(packagesDir, pkg.Existing.ID, pkg.Existing.Version)
		staging := stagedInstallPackageDir(packagesDir, pkg)
		backup := backupInstallPackageDir(packagesDir, pkg)
		if _, err := os.Stat(backup); err == nil {
			return promoted, fmt.Errorf("package backup directory already exists: %s", backup)
		} else if !os.IsNotExist(err) {
			return promoted, fmt.Errorf("stat package backup directory %s: %w", backup, err)
		}
		if err := os.Rename(final, backup); err != nil {
			return promoted, fmt.Errorf("backup existing package directory %s: %w", final, err)
		}
		swap := promotedPackageDir{Final: final, Backup: backup}
		promoted = append(promoted, swap)
		if err := os.Rename(staging, final); err != nil {
			if restoreErr := os.Rename(backup, final); restoreErr != nil {
				return promoted, fmt.Errorf("promote package directory %s: %w; restore backup: %v", final, err, restoreErr)
			}
			promoted = promoted[:len(promoted)-1]
			return promoted, fmt.Errorf("promote package directory %s: %w", final, err)
		}
	}
	return promoted, nil
}

func rollbackPromotedPackageDirs(promoted []promotedPackageDir) {
	for i := len(promoted) - 1; i >= 0; i-- {
		_ = os.RemoveAll(promoted[i].Final)
		_ = os.Rename(promoted[i].Backup, promoted[i].Final)
	}
}

func cleanupPromotedPackageDirBackups(promoted []promotedPackageDir) {
	for _, dir := range promoted {
		_ = os.RemoveAll(dir.Backup)
	}
}

func backupInstallPackageDir(packagesDir string, pkg installPackage) string {
	hashPrefix := pkg.Hash
	if len(hashPrefix) > 16 {
		hashPrefix = hashPrefix[:16]
	}
	return filepath.Join(packagesDir, "."+installedPackageDirName(pkg.Inspection.ID, pkg.Inspection.Version.String())+".old-"+hashPrefix)
}

func installPackageKey(id string, version string) string {
	return id + "\x00" + version
}

func installPackageRef(pkg installPackage) string {
	return pkg.Inspection.ID + "@" + pkg.Inspection.Version.String()
}

func installSingerRef(pkg installPackage, singerID string) string {
	return installPackageRef(pkg) + "[" + singerID + "]"
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func installStringPointer(value string) *string {
	return &value
}

func errorCodeForInstallError(err error) string {
	var missing installDependencyMissingError
	if errors.As(err, &missing) {
		return "DEPENDENCY_MISSING"
	}
	var cycle installDependencyCycleError
	if errors.As(err, &cycle) {
		return "CYCLE_DEPENDENCY"
	}
	return "IO_ERROR"
}

type installDependencyMissingError struct {
	Package    string
	Dependency string
}

func (e installDependencyMissingError) Error() string {
	return fmt.Sprintf("%s requires missing dependency %s", e.Package, e.Dependency)
}

type installDependencyCycleError struct {
	Cycle []string
}

func (e installDependencyCycleError) Error() string {
	return fmt.Sprintf("dependency cycle detected: %s", strings.Join(e.Cycle, " -> "))
}

func newInstallReporter(jsonOutput bool, out io.Writer) installReporter {
	if jsonOutput {
		return &installJSONReporter{out: out, encoder: json.NewEncoder(out)}
	}
	return newInstallTextReporter(out)
}

type installJSONReporter struct {
	out     io.Writer
	encoder *json.Encoder
}

func (r *installJSONReporter) InspectStart(paths []string) {
	for _, path := range paths {
		r.event("INSPECT_START", map[string]any{"packageFile": path})
	}
}

func (r *installJSONReporter) InspectOK(index int, pkg installPackage) {
	r.event("INSPECT_OK", map[string]any{"packageFile": pkg.Path, "package": installPackageRef(pkg), "hash": pkg.Hash})
}

func (r *installJSONReporter) InspectFail(index int, path string, err error) {
	r.event("INSPECT_FAILED", map[string]any{"packageFile": path, "message": err.Error()})
}

func (r *installJSONReporter) Notice(message string) {
	r.event("NOTICE", map[string]any{"message": message})
}

func (r *installJSONReporter) DryRun(pkgs []installPackage) {
	for _, pkg := range pkgs {
		r.event("DRY_RUN_PACKAGE", installPackageEventData(pkg))
	}
	r.event("SUMMARY", installSummaryData(pkgs))
}

func (r *installJSONReporter) ExtractStart(pkgs []installPackage) {
	for _, pkg := range pkgs {
		if pkg.Action != installActionSkip {
			r.event("EXTRACT_START", installPackageEventData(pkg))
		}
	}
}

func (r *installJSONReporter) ExtractProgress(index int, progress packagearchive.ExtractProgress) {
	r.event("EXTRACT_PROGRESS", map[string]any{
		"index":    index,
		"filePath": progress.FilePath,
		"current":  progress.Current,
		"total":    progress.Total,
	})
}

func (r *installJSONReporter) ExtractDone(index int, pkg installPackage) {
	r.event("EXTRACT_DONE", installPackageEventData(pkg))
}

func (r *installJSONReporter) Result(pkg installPackage) {
	r.event("RESULT", installPackageEventData(pkg))
}

func (r *installJSONReporter) Summary(installed int, overwritten int, skipped int) {
	r.event("SUMMARY", map[string]any{"installed": installed, "overwritten": overwritten, "skipped": skipped})
}

func (r *installJSONReporter) Error(code string, message string, details map[string]any, err error) error {
	_ = r.encoder.Encode(installEvent{
		Command: "install",
		Event:   "ERROR",
		Error: &inspectError{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
	return err
}

func (r *installJSONReporter) event(event string, data any) {
	_ = r.encoder.Encode(installEvent{Command: "install", Event: event, Data: data})
}

func installPackageEventData(pkg installPackage) map[string]any {
	return map[string]any{
		"packageFile": pkg.Path,
		"id":          pkg.Inspection.ID,
		"version":     pkg.Inspection.Version.String(),
		"package":     installPackageRef(pkg),
		"hash":        pkg.Hash,
		"action":      string(pkg.Action),
	}
}

func installSummaryData(pkgs []installPackage) map[string]any {
	counts := map[string]int{"install": 0, "overwrite": 0, "skip": 0}
	for _, pkg := range pkgs {
		counts[string(pkg.Action)]++
	}
	return map[string]any{"counts": counts}
}

type installTextReporter struct {
	out             io.Writer
	useLive         bool
	inspectMulti    *pterm.MultiPrinter
	inspectSpinners []*pterm.SpinnerPrinter
	extractMulti    *pterm.MultiPrinter
	extractBars     map[int]*pterm.ProgressbarPrinter
	extractTitles   map[int]io.Writer
	extractNames    map[int]string
	mu              sync.Mutex
}

type installStatusPrinter struct {
	icon         string
	iconStyle    lipgloss.Style
	messageStyle lipgloss.Style
	colorMessage bool
	writer       io.Writer
}

func (p installStatusPrinter) Sprint(a ...any) string {
	message := fmt.Sprint(a...)
	if !p.colorMessage {
		return fmt.Sprintf("%s %s", p.iconStyle.Render(p.icon), message)
	}
	return fmt.Sprintf("%s %s", p.iconStyle.Render(p.icon), p.messageStyle.Render(message))
}

func (p installStatusPrinter) Sprintln(a ...any) string {
	return p.Sprint(a...) + "\n"
}

func (p installStatusPrinter) Sprintf(format string, a ...any) string {
	return p.Sprint(fmt.Sprintf(format, a...))
}

func (p installStatusPrinter) Sprintfln(format string, a ...any) string {
	return p.Sprintf(format, a...) + "\n"
}

func (p installStatusPrinter) Print(a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	fmt.Fprint(p.writer, p.Sprint(a...))
	return &textPrinter
}

func (p installStatusPrinter) Println(a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	fmt.Fprint(p.writer, p.Sprintln(a...))
	return &textPrinter
}

func (p installStatusPrinter) Printf(format string, a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	fmt.Fprint(p.writer, p.Sprintf(format, a...))
	return &textPrinter
}

func (p installStatusPrinter) Printfln(format string, a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	fmt.Fprint(p.writer, p.Sprintfln(format, a...))
	return &textPrinter
}

func (p installStatusPrinter) PrintOnError(a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	for _, value := range a {
		if err, ok := value.(error); ok && err != nil {
			fmt.Fprint(p.writer, p.Sprintln(err))
		}
	}
	return &textPrinter
}

func (p installStatusPrinter) PrintOnErrorf(format string, a ...any) *pterm.TextPrinter {
	textPrinter := pterm.TextPrinter(p)
	for _, value := range a {
		if err, ok := value.(error); ok && err != nil {
			fmt.Fprint(p.writer, p.Sprintfln(format, err))
		}
	}
	return &textPrinter
}

func newInstallTextReporter(out io.Writer) *installTextReporter {
	return &installTextReporter{
		out:           out,
		useLive:       installCanUseLiveOutput(out),
		extractBars:   make(map[int]*pterm.ProgressbarPrinter),
		extractTitles: make(map[int]io.Writer),
		extractNames:  make(map[int]string),
	}
}

func installCanUseLiveOutput(out io.Writer) bool {
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func (r *installTextReporter) InspectStart(paths []string) {
	if !r.useLive {
		for _, path := range paths {
			fmt.Fprintf(r.out, "Inspecting %s\n", path)
		}
		return
	}
	multi := pterm.DefaultMultiPrinter.WithWriter(r.out)
	r.inspectMulti = multi
	r.inspectSpinners = make([]*pterm.SpinnerPrinter, len(paths))
	for index, path := range paths {
		spinnerPrinter := pterm.DefaultSpinner.
			WithWriter(multi.NewWriter()).
			WithShowTimer(false)
		spinnerPrinter.SuccessPrinter = installStatusPrinter{icon: "✓", iconStyle: installOKStyle, writer: r.out}
		spinnerPrinter.FailPrinter = installStatusPrinter{icon: "✗", iconStyle: installErrorStyle, messageStyle: installErrorStyle, colorMessage: true, writer: r.out}
		spinner, _ := spinnerPrinter.Start("Inspecting " + path)
		r.inspectSpinners[index] = spinner
	}
	_, _ = multi.Start()
}

func (r *installTextReporter) InspectOK(index int, pkg installPackage) {
	if r.useLive && index < len(r.inspectSpinners) && r.inspectSpinners[index] != nil {
		r.inspectSpinners[index].Success("Inspecting " + pkg.Path)
		return
	}
	fmt.Fprintf(r.out, "%s Inspecting %s\n", installOKStyle.Render("✓"), pkg.Path)
}

func (r *installTextReporter) InspectFail(index int, path string, err error) {
	if r.useLive && index < len(r.inspectSpinners) && r.inspectSpinners[index] != nil {
		r.inspectSpinners[index].Fail("Inspecting " + path)
		if r.inspectMulti != nil {
			_, _ = r.inspectMulti.Stop()
		}
	} else {
		fmt.Fprintf(r.out, "%s\n", installErrorStyle.Render("✗ Inspecting "+path))
	}
	fmt.Fprintf(r.out, "%s\n", installMutedStyle.Render("  "+err.Error()))
}

func (r *installTextReporter) Notice(message string) {
	r.stopInspectMulti()
	fmt.Fprintln(r.out, installWarnStyle.Render(message))
}

func (r *installTextReporter) DryRun(pkgs []installPackage) {
	r.stopInspectMulti()
	installed, overwritten, skipped := 0, 0, 0
	for _, pkg := range pkgs {
		fmt.Fprintf(r.out, "%s %s [%s] [hash=%s]\n", installWarnStyle.Render("[DRY RUN]"), installPackageRef(pkg), pkg.Action, pkg.Hash)
		switch pkg.Action {
		case installActionOverwrite:
			overwritten++
		case installActionSkip:
			skipped++
		default:
			installed++
		}
	}
	fmt.Fprintf(r.out, "Summary (dry run): %d installed, %d overwritten, %d skipped\n", installed, overwritten, skipped)
}

func (r *installTextReporter) ExtractStart(pkgs []installPackage) {
	r.stopInspectMulti()
	if !r.useLive {
		return
	}
	multi := pterm.DefaultMultiPrinter.WithWriter(r.out).WithUpdateDelay(100 * time.Millisecond)
	r.extractMulti = multi
	for index, pkg := range pkgs {
		if pkg.Action == installActionSkip {
			continue
		}
		titleWriter := multi.NewWriter()
		title := fmt.Sprintf("%s: extracting", installPackageStyle.Render(installPackageRef(pkg)))
		fmt.Fprint(titleWriter, title)
		bar, _ := pterm.DefaultProgressbar.
			WithWriter(multi.NewWriter()).
			WithTotal(1000).
			WithMaxWidth(0).
			WithShowElapsedTime(false).
			WithShowCount(false).
			WithShowTitle(false).
			WithTitle(installPackageRef(pkg) + ": extracting").
			Start()
		r.extractBars[index] = bar
		r.extractTitles[index] = titleWriter
		r.extractNames[index] = installPackageRef(pkg)
	}
	_, _ = multi.Start()
}

func (r *installTextReporter) ExtractProgress(index int, progress packagearchive.ExtractProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useLive {
		bar := r.extractBars[index]
		if bar == nil {
			return
		}
		title := fmt.Sprintf("%s: extracting %s", installPackageStyle.Render(r.extractNames[index]), progress.FilePath)
		if titleWriter := r.extractTitles[index]; titleWriter != nil {
			fmt.Fprint(titleWriter, "\r"+title)
		}
		if progress.Total > 0 {
			current := int((progress.Current * 1000) / progress.Total)
			if current > bar.Current {
				bar.Add(current - bar.Current)
			}
		}
		return
	}
	if progress.FilePath != "" {
		fmt.Fprintf(r.out, "extracting %s (%d/%d)\n", progress.FilePath, progress.Current, progress.Total)
	}
}

func (r *installTextReporter) ExtractDone(index int, pkg installPackage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useLive {
		if bar := r.extractBars[index]; bar != nil && bar.Current < bar.Total {
			bar.Add(bar.Total - bar.Current)
		}
	}
}

func (r *installTextReporter) Result(pkg installPackage) {
	r.stopExtractMulti()
	switch pkg.Action {
	case installActionSkip:
		fmt.Fprintf(r.out, "%s %s [skipped] [hash=%s]\n", installWarnStyle.Render("!"), installPackageRef(pkg), pkg.Hash)
	case installActionOverwrite:
		fmt.Fprintf(r.out, "%s %s [overwritten] [hash=%s]\n", installOKStyle.Render("✓"), installPackageRef(pkg), pkg.Hash)
	default:
		fmt.Fprintf(r.out, "%s %s [installed] [hash=%s]\n", installOKStyle.Render("✓"), installPackageRef(pkg), pkg.Hash)
	}
}

func (r *installTextReporter) Summary(installed int, overwritten int, skipped int) {
	fmt.Fprintf(r.out, "Summary: %d installed, %d overwritten, %d skipped\n", installed, overwritten, skipped)
}

func (r *installTextReporter) Error(code string, message string, details map[string]any, err error) error {
	r.stopInspectMulti()
	r.stopExtractMulti()
	fmt.Fprintln(r.out, installErrorStyle.Render(message))
	return err
}

func (r *installTextReporter) stopInspectMulti() {
	if r.inspectMulti != nil && r.inspectMulti.IsActive {
		_, _ = r.inspectMulti.Stop()
	}
}

func (r *installTextReporter) stopExtractMulti() {
	if r.extractMulti != nil && r.extractMulti.IsActive {
		_, _ = r.extractMulti.Stop()
	}
}
