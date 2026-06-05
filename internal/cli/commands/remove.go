package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
	"github.com/diffscope/diffscope-package-manager/packageinfo"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

type removeCategory string

const (
	removeCategoryDirect  removeCategory = "direct"
	removeCategoryCascade removeCategory = "cascade"
)

type removePackage struct {
	Package  model.Package
	Category removeCategory
}

type removeIgnoredPackage struct {
	Reference string
	Reason    string
}

type removeEvent struct {
	Command string        `json:"command"`
	Event   string        `json:"event"`
	Data    any           `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type removeReporter interface {
	Plan(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage)
	DryRun(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage)
	RemoveStart(pkg removePackage)
	Result(pkg removePackage)
	Summary(removed int, ignored int, direct int, cascade int, dryRun bool)
	Error(code string, message string, details map[string]any, err error) error
}

// NewRemoveCmd creates the remove command.
func NewRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <package>...",
		Aliases: []string{"rm", "uninstall"},
		Short:   "Remove one or more packages",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cascade, err := cmd.Flags().GetBool("cascade")
			if err != nil {
				return err
			}
			ignoreNonExistent, err := cmd.Flags().GetBool("ignore-non-existent")
			if err != nil {
				return err
			}
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			return RemovePackages(
				args,
				viper.GetString("packages_dir"),
				cascade,
				ignoreNonExistent,
				dryRun,
				viper.GetBool("json"),
				cmd.OutOrStdout(),
			)
		},
	}

	cmd.Flags().Bool("cascade", false, "remove dependent packages recursively")
	cmd.Flags().Bool("ignore-non-existent", false, "ignore non-existent packages and continue removing others")
	cmd.Flags().Bool("dry-run", false, "check and report packages without removing them")
	return cmd
}

// RemovePackages is the command entrypoint for removing installed packages.
func RemovePackages(targets []string, packagesDir string, cascade bool, ignoreNonExistent bool, dryRun bool, jsonOutput bool, out io.Writer) error {
	reporter := newRemoveReporter(jsonOutput, out)

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	defer sqlDB.Close()

	direct, ignored, err := resolveRemoveTargets(db, targets, ignoreNonExistent)
	if err != nil {
		return writeRemoveLoadError(reporter, err)
	}
	cascadePackages, err := findCascadeRemovePackages(db, direct)
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}

	reporter.Plan(direct, cascadePackages, ignored)
	if dryRun {
		reporter.DryRun(direct, cascadePackages, ignored)
		return nil
	}
	if len(cascadePackages) > 0 && !cascade {
		err := removeDependencyExistsError{Packages: cascadePackages}
		return reporter.Error("DEPENDENCY_EXISTS", err.Error(), map[string]any{"packages": removePackagesJSON(cascadePackages)}, err)
	}

	toRemove := append(append([]removePackage(nil), direct...), cascadePackages...)
	locks, err := lockRemovePackageDirs(packagesDir, toRemove)
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	defer unlockRemovePackageDirs(locks)

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

	txDB := db.Session(&gorm.Session{SkipDefaultTransaction: true})
	if err := deleteRemovePackageRows(txDB, toRemove); err != nil {
		return reporter.Error("IO_ERROR", err.Error(), nil, err)
	}
	if err := db.Exec("COMMIT").Error; err != nil {
		return reporter.Error("IO_ERROR", fmt.Sprintf("commit remove transaction: %v", err), nil, err)
	}
	committed = true

	removed := 0
	for _, pkg := range toRemove {
		reporter.RemoveStart(pkg)
		if err := os.RemoveAll(installedPackageDir(packagesDir, pkg.Package.ID, pkg.Package.Version)); err != nil {
			return reporter.Error("IO_ERROR", fmt.Sprintf("remove package directory for %s: %v", removePackageRef(pkg.Package), err), nil, err)
		}
		reporter.Result(pkg)
		removed++
	}
	reporter.Summary(removed, len(ignored), len(direct), len(cascadePackages), false)
	return nil
}

func resolveRemoveTargets(db *gorm.DB, targets []string, ignoreNonExistent bool) ([]removePackage, []removeIgnoredPackage, error) {
	direct := make([]removePackage, 0, len(targets))
	ignored := make([]removeIgnoredPackage, 0)
	seen := make(map[string]struct{}, len(targets))
	ignoredSeen := make(map[string]struct{}, len(targets))

	for _, target := range targets {
		reference, err := packageinfo.ParsePackageReference(target)
		if err != nil {
			return nil, nil, err
		}
		if reference.Type != packageinfo.PackageReferenceTypePackage || reference.PackageID == "" {
			return nil, nil, fmt.Errorf("remove target %q must be a package reference", target)
		}

		pkg, err := resolveRemovePackage(db, reference)
		if err != nil {
			var notFound infoNotFoundError
			if ignoreNonExistent && errors.As(err, &notFound) {
				key := reference.String()
				if key == "" {
					key = target
				}
				if _, ok := ignoredSeen[key]; !ok {
					ignored = append(ignored, removeIgnoredPackage{Reference: key, Reason: "not installed"})
					ignoredSeen[key] = struct{}{}
				}
				continue
			}
			return nil, nil, err
		}

		key := removePackageKey(pkg.ID, pkg.Version)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		direct = append(direct, removePackage{Package: pkg, Category: removeCategoryDirect})
	}

	return direct, ignored, nil
}

func resolveRemovePackage(db *gorm.DB, reference packageinfo.PackageReference) (model.Package, error) {
	resolved, err := resolveInfoPackageVersion(db, reference)
	if err != nil {
		return model.Package{}, err
	}
	var packages []model.Package
	err = db.Where("id = ? AND version = ?", resolved.PackageID, resolved.Version.String()).Limit(1).Find(&packages).Error
	if err != nil {
		return model.Package{}, err
	}
	if len(packages) == 0 {
		return model.Package{}, infoNotFoundError{Message: fmt.Sprintf("package %q is not installed", resolved.String())}
	}
	return packages[0], nil
}

func findCascadeRemovePackages(db *gorm.DB, direct []removePackage) ([]removePackage, error) {
	selected := make(map[string]struct{}, len(direct))
	queue := make([]model.Package, 0, len(direct))
	for _, pkg := range direct {
		key := removePackageKey(pkg.Package.ID, pkg.Package.Version)
		selected[key] = struct{}{}
		queue = append(queue, pkg.Package)
	}

	var cascade []removePackage
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		var rows []model.Dependency
		if err := db.
			Where("dependent_package_id = ? AND dependent_package_version = ?", current.ID, current.Version).
			Order("package_id ASC, package_version ASC").
			Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := removePackageKey(row.PackageID, row.PackageVersion)
			if _, ok := selected[key]; ok {
				continue
			}
			var dependent model.Package
			if err := db.Where("id = ? AND version = ?", row.PackageID, row.PackageVersion).First(&dependent).Error; err != nil {
				return nil, err
			}
			selected[key] = struct{}{}
			cascade = append(cascade, removePackage{Package: dependent, Category: removeCategoryCascade})
			queue = append(queue, dependent)
		}
	}

	sort.SliceStable(cascade, func(i, j int) bool {
		return removePackageRef(cascade[i].Package) < removePackageRef(cascade[j].Package)
	})
	return cascade, nil
}

func lockRemovePackageDirs(packagesDir string, packages []removePackage) ([]*packagedatabase.PackageDirLock, error) {
	locks := make([]*packagedatabase.PackageDirLock, 0, len(packages))
	seen := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		dir := installedPackageDir(packagesDir, pkg.Package.ID, pkg.Package.Version)
		key, err := filepath.Abs(dir)
		if err != nil {
			unlockRemovePackageDirs(locks)
			return nil, fmt.Errorf("resolve package directory %s: %w", dir, err)
		}
		key = strings.ToLower(filepath.Clean(key))
		if _, ok := seen[key]; ok {
			continue
		}
		lock, err := packagedatabase.LockPackageDir(dir)
		if err != nil {
			unlockRemovePackageDirs(locks)
			return nil, err
		}
		locks = append(locks, lock)
		seen[key] = struct{}{}
	}
	return locks, nil
}

func unlockRemovePackageDirs(locks []*packagedatabase.PackageDirLock) {
	for index := len(locks) - 1; index >= 0; index-- {
		_ = locks[index].Unlock()
	}
}

func deleteRemovePackageRows(db *gorm.DB, packages []removePackage) error {
	for _, pkg := range packages {
		if err := db.Where("id = ? AND version = ?", pkg.Package.ID, pkg.Package.Version).Delete(&model.Package{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func removePackageKey(id string, version string) string {
	return id + "\x00" + version
}

func removePackageRef(pkg model.Package) string {
	return pkg.ID + "@" + pkg.Version
}

type removeDependencyExistsError struct {
	Packages []removePackage
}

func (e removeDependencyExistsError) Error() string {
	refs := make([]string, 0, len(e.Packages))
	for _, pkg := range e.Packages {
		refs = append(refs, removePackageRef(pkg.Package))
	}
	return fmt.Sprintf("dependent packages would also be removed; use --cascade to remove them: %s", strings.Join(refs, ", "))
}

func writeRemoveLoadError(reporter removeReporter, err error) error {
	var ambiguous infoAmbiguousVersionError
	if errors.As(err, &ambiguous) {
		details := map[string]any{
			"package":    ambiguous.PackageID,
			"candidates": ambiguous.Candidates,
		}
		return reporter.Error("AMBIGUOUS_VERSION", ambiguous.Error(), details, err)
	}

	var notFound infoNotFoundError
	if errors.As(err, &notFound) {
		return reporter.Error("NOT_FOUND", notFound.Error(), nil, err)
	}

	return reporter.Error("SCHEMA_ERROR", err.Error(), nil, err)
}

func newRemoveReporter(jsonOutput bool, out io.Writer) removeReporter {
	if jsonOutput {
		return &removeJSONReporter{encoder: json.NewEncoder(out)}
	}
	return &removeTextReporter{out: out}
}

type removeJSONReporter struct {
	encoder *json.Encoder
}

func (r *removeJSONReporter) Plan(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage) {
	for _, pkg := range direct {
		r.event("PLAN_PACKAGE", removePackageEventData(pkg))
	}
	for _, pkg := range cascade {
		r.event("PLAN_PACKAGE", removePackageEventData(pkg))
	}
	for _, item := range ignored {
		r.event("IGNORED_PACKAGE", map[string]any{"package": item.Reference, "reason": item.Reason})
	}
}

func (r *removeJSONReporter) DryRun(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage) {
	for _, pkg := range direct {
		r.event("DRY_RUN_PACKAGE", removePackageEventData(pkg))
	}
	for _, pkg := range cascade {
		r.event("DRY_RUN_PACKAGE", removePackageEventData(pkg))
	}
	r.Summary(0, len(ignored), len(direct), len(cascade), true)
}

func (r *removeJSONReporter) RemoveStart(pkg removePackage) {
	r.event("REMOVE_START", removePackageEventData(pkg))
}

func (r *removeJSONReporter) Result(pkg removePackage) {
	data := removePackageEventData(pkg)
	data["action"] = "removed"
	r.event("RESULT", data)
}

func (r *removeJSONReporter) Summary(removed int, ignored int, direct int, cascade int, dryRun bool) {
	r.event("SUMMARY", map[string]any{
		"removed": removed,
		"ignored": ignored,
		"direct":  direct,
		"cascade": cascade,
		"planned": direct + cascade,
		"dryRun":  dryRun,
	})
}

func (r *removeJSONReporter) Error(code string, message string, details map[string]any, err error) error {
	_ = r.encoder.Encode(removeEvent{
		Command: "remove",
		Event:   "ERROR",
		Error: &inspectError{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
	return err
}

func (r *removeJSONReporter) event(event string, data any) {
	_ = r.encoder.Encode(removeEvent{Command: "remove", Event: event, Data: data})
}

type removeTextReporter struct {
	out io.Writer
}

func (r *removeTextReporter) Plan(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage) {
	if len(direct) > 0 {
		fmt.Fprintln(r.out, "Packages to remove:")
		for _, pkg := range direct {
			fmt.Fprintf(r.out, "  %s\n", removePackageRef(pkg.Package))
		}
	}
	if len(cascade) > 0 {
		fmt.Fprintln(r.out, "Packages to remove by cascade:")
		for _, pkg := range cascade {
			fmt.Fprintf(r.out, "  %s\n", removePackageRef(pkg.Package))
		}
	}
	if len(ignored) > 0 {
		fmt.Fprintln(r.out, "Ignored non-existent packages:")
		for _, item := range ignored {
			fmt.Fprintf(r.out, "  %s\n", item.Reference)
		}
	}
}

func (r *removeTextReporter) DryRun(direct []removePackage, cascade []removePackage, ignored []removeIgnoredPackage) {
	for _, pkg := range direct {
		fmt.Fprintf(r.out, "%s %s [direct] [hash=%s]\n", installWarnStyle.Render("[DRY RUN]"), removePackageRef(pkg.Package), pkg.Package.Hash)
	}
	for _, pkg := range cascade {
		fmt.Fprintf(r.out, "%s %s [cascade] [hash=%s]\n", installWarnStyle.Render("[DRY RUN]"), removePackageRef(pkg.Package), pkg.Package.Hash)
	}
	fmt.Fprintf(r.out, "Summary (dry run): %d removed, %d ignored\n", len(direct)+len(cascade), len(ignored))
}

func (r *removeTextReporter) RemoveStart(pkg removePackage) {
}

func (r *removeTextReporter) Result(pkg removePackage) {
	fmt.Fprintf(r.out, "%s %s [removed]\n", installOKStyle.Render("✓"), removePackageRef(pkg.Package))
}

func (r *removeTextReporter) Summary(removed int, ignored int, direct int, cascade int, dryRun bool) {
	if dryRun {
		fmt.Fprintf(r.out, "Summary: %d planned, %d ignored\n", direct+cascade, ignored)
		return
	}
	fmt.Fprintf(r.out, "Summary: %d removed, %d ignored\n", removed, ignored)
}

func (r *removeTextReporter) Error(code string, message string, details map[string]any, err error) error {
	fmt.Fprintln(r.out, installErrorStyle.Render(message))
	return err
}

func removePackageEventData(pkg removePackage) map[string]any {
	return map[string]any{
		"id":       pkg.Package.ID,
		"version":  pkg.Package.Version,
		"package":  removePackageRef(pkg.Package),
		"hash":     pkg.Package.Hash,
		"category": string(pkg.Category),
		"action":   "remove",
	}
}

func removePackagesJSON(packages []removePackage) []map[string]any {
	items := make([]map[string]any, 0, len(packages))
	for _, pkg := range packages {
		items = append(items, removePackageEventData(pkg))
	}
	return items
}
