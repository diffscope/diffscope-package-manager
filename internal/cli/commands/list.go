package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	internallanguage "github.com/diffscope/diffscope-package-manager/internal/language"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
	"github.com/diffscope/diffscope-package-manager/packageinfo"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

var (
	listHeaderStyle = lipgloss.NewStyle().Bold(true)
)

type listOutput struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Data    listData      `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type listData struct {
	Packages []listPackageJSON `json:"packages"`
}

type listPackageJSON struct {
	ID          string `json:"id,omitempty"`
	Version     string `json:"version,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Name        any    `json:"name,omitempty"`
	InstalledAt string `json:"installedAt,omitempty"`
}

type listPackageRow struct {
	Package model.Package
	Name    *packageinfo.MultilingualText
}

type listColumn struct {
	key    string
	header string
}

var listColumns = map[string]listColumn{
	"id":           {key: "id", header: "ID"},
	"version":      {key: "version", header: "Version"},
	"hash":         {key: "hash", header: "Hash"},
	"name":         {key: "name", header: "Name"},
	"installed_at": {key: "installed_at", header: "InstalledAt"},
	"installedat":  {key: "installed_at", header: "InstalledAt"},
}

// NewListCmd creates the list command.
func NewListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed packages",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			languageCode, err := cmd.Flags().GetString("language")
			if err != nil {
				return err
			}
			columnText, err := cmd.Flags().GetString("column")
			if err != nil {
				return err
			}
			return ListPackages(viper.GetString("packages_dir"), languageCode, columnText, viper.GetBool("json"), cmd.OutOrStdout())
		},
	}

	cmd.Flags().String("column", "id,version", "comma-separated columns to display: id, version, hash, name, installed_at")
	cmd.Flags().String("language", "", "language tag or '*' for all languages")
	return cmd
}

// ListPackages is the command entrypoint for listing installed packages.
func ListPackages(packagesDir string, languageCode string, columnText string, jsonOutput bool, out io.Writer) error {
	columns, err := parseListColumns(columnText)
	if err != nil {
		return writeListError(jsonOutput, out, "INVALID_COLUMN", err.Error(), err)
	}
	if languageCode == "" {
		languageCode = internallanguage.CurrentCode()
	}

	db, err := packagedatabase.Open(filepath.Join(packagesDir, "packages.db"))
	if err != nil {
		return writeListError(jsonOutput, out, "IO_ERROR", err.Error(), err)
	}
	if sqlDB, err := db.DB(); err == nil {
		defer sqlDB.Close()
	}

	rows, err := loadListPackageRows(db)
	if err != nil {
		return writeListError(jsonOutput, out, "IO_ERROR", err.Error(), err)
	}

	if jsonOutput {
		return json.NewEncoder(out).Encode(listOutput{
			OK:      true,
			Command: "list",
			Data: listData{
				Packages: buildListPackageJSON(rows, columns, languageCode),
			},
		})
	}

	printListTable(out, rows, columns, languageCode)
	return nil
}

func parseListColumns(columnText string) ([]listColumn, error) {
	if strings.TrimSpace(columnText) == "" {
		columnText = "id,version"
	}

	parts := strings.Split(columnText, ",")
	columns := make([]listColumn, 0, len(parts))
	for _, part := range parts {
		key := strings.ToLower(strings.TrimSpace(part))
		column, ok := listColumns[key]
		if !ok {
			return nil, fmt.Errorf("unknown list column %q", strings.TrimSpace(part))
		}
		columns = append(columns, column)
	}
	return columns, nil
}

func loadListPackageRows(db *gorm.DB) ([]listPackageRow, error) {
	var packages []model.Package
	if err := db.Find(&packages).Error; err != nil {
		return nil, err
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].ID != packages[j].ID {
			return packages[i].ID < packages[j].ID
		}
		return compareListVersions(packages[i].Version, packages[j].Version) > 0
	})

	var infos []model.PackageMultilingualInfo
	if err := db.Where("name IS NOT NULL").Find(&infos).Error; err != nil {
		return nil, err
	}
	names := make(map[string]packageinfo.MultilingualText, len(infos))
	for _, info := range infos {
		if info.Name == nil {
			continue
		}
		key := listPackageKey(info.PackageID, info.PackageVersion)
		name := names[key]
		if name.Texts == nil {
			name.Texts = make(map[string]string)
		}
		addMultilingualName(&name, info.Language, *info.Name)
		names[key] = name
	}

	rows := make([]listPackageRow, 0, len(packages))
	for _, pkg := range packages {
		rows = append(rows, listPackageRow{
			Package: pkg,
			Name:    normalizeMultilingualName(names[listPackageKey(pkg.ID, pkg.Version)]),
		})
	}
	return rows, nil
}

func compareListVersions(left string, right string) int {
	leftVersion, leftErr := packageinfo.ParsePackageVersion(left)
	rightVersion, rightErr := packageinfo.ParsePackageVersion(right)
	if leftErr == nil && rightErr == nil {
		return leftVersion.Compare(rightVersion)
	}
	return strings.Compare(left, right)
}

func listPackageKey(id string, version string) string {
	return id + "\x00" + version
}

func buildListPackageJSON(rows []listPackageRow, columns []listColumn, languageCode string) []listPackageJSON {
	packages := make([]listPackageJSON, 0, len(rows))
	for _, row := range rows {
		var item listPackageJSON
		for _, column := range columns {
			switch column.key {
			case "id":
				item.ID = row.Package.ID
			case "version":
				item.Version = row.Package.Version
			case "hash":
				item.Hash = row.Package.Hash
			case "name":
				item.Name = multilingualJSONValue(row.Name, languageCode)
			case "installed_at":
				item.InstalledAt = formatInstalledAtJSON(row.Package.InstalledAt)
			}
		}
		packages = append(packages, item)
	}
	return packages
}

func printListTable(out io.Writer, rows []listPackageRow, columns []listColumn, languageCode string) {
	headers := make([]string, 0, len(columns))
	for _, column := range columns {
		headers = append(headers, column.header)
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderHeader(true).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return listHeaderStyle.PaddingRight(2)
			}
			return lipgloss.NewStyle().PaddingRight(2)
		}).
		Headers(headers...)

	for _, row := range rows {
		cells := make([]string, 0, len(columns))
		for _, column := range columns {
			cells = append(cells, listColumnValue(row, column.key, languageCode))
		}
		t.Row(cells...)
	}

	fmt.Fprintln(out, t.Render())
}

func listColumnValue(row listPackageRow, columnKey string, languageCode string) string {
	switch columnKey {
	case "id":
		return row.Package.ID
	case "version":
		return row.Package.Version
	case "hash":
		return row.Package.Hash
	case "name":
		return listNameText(row.Name, languageCode)
	case "installed_at":
		return formatInstalledAtText(row.Package.InstalledAt)
	default:
		return ""
	}
}

func listNameText(name *packageinfo.MultilingualText, languageCode string) string {
	if name == nil {
		return ""
	}
	if languageCode == "*" {
		values := multilingualTextMap(name)
		lines := make([]string, 0, len(values))
		for _, languageKey := range sortedMultilingualKeys(name) {
			tag := inspectLanguageTagStyle.Render(fmt.Sprintf("[%s]", languageKey))
			lines = append(lines, fmt.Sprintf("%s %s", tag, values[languageKey]))
		}
		return strings.Join(lines, "\n")
	}
	return selectMultilingualText(name, languageCode)
}

func writeListError(jsonOutput bool, out io.Writer, code string, message string, err error) error {
	if jsonOutput {
		encodeErr := json.NewEncoder(out).Encode(listOutput{
			OK:      false,
			Command: "list",
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
