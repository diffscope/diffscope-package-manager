package output

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/diffscope/diffscope-package-manager/internal/language"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

// Text styling belongs to the destination writer, never to the JSON renderer
// or a process-global terminal profile. Redirected output stays readable.
type textStyles struct {
	renderer                                                           *lipgloss.Renderer
	section, key, language, muted, success, warning, failure, identity lipgloss.Style
}

func newTextStyles(out io.Writer) *textStyles {
	r := lipgloss.NewRenderer(out)
	return &textStyles{
		renderer: r,
		section:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("6")),
		key:      r.NewStyle().Foreground(lipgloss.Color("6")),
		language: r.NewStyle().Foreground(lipgloss.Color("5")),
		muted:    r.NewStyle().Faint(true),
		success:  r.NewStyle().Foreground(lipgloss.Color("2")),
		warning:  r.NewStyle().Foreground(lipgloss.Color("3")),
		failure:  r.NewStyle().Foreground(lipgloss.Color("1")),
		identity: r.NewStyle().Bold(true),
	}
}

func (r *Renderer) styles() *textStyles {
	if r.style == nil {
		r.style = newTextStyles(r.Out)
	}
	return r.style
}
func (r *Renderer) errorStyles() *textStyles {
	if r.errStyle == nil {
		r.errStyle = newTextStyles(r.Err)
	}
	return r.errStyle
}
func (r *Renderer) printf(format string, args ...any) {
	if r.writeError == nil {
		_, r.writeError = fmt.Fprintf(r.Out, format, args...)
	}
}
func (r *Renderer) section(title string) { r.printf("%s\n", r.styles().section.Render(title)) }
func (r *Renderer) field(indent, key, value string) {
	r.printf("%s%s %s\n", indent, r.styles().key.Render(key+":"), value)
}
func textKeys(text packageinfo.Text) []string {
	keys := make([]string, 0, len(text))
	for key := range text {
		if key != "_" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if _, ok := text["_"]; ok {
		keys = append([]string{"_"}, keys...)
	}
	return keys
}
func (r *Renderer) Text(text packageinfo.Text) string {
	if r.Language == "*" {
		lines := []string{}
		for _, key := range textKeys(text) {
			lines = append(lines, r.styles().language.Render("["+key+"]")+" "+text[key])
		}
		return strings.Join(lines, "\n")
	}
	if value, ok := text[r.Language]; ok {
		return value
	}
	keys := textKeys(text)
	if len(keys) > 0 && keys[0] == "_" {
		keys = keys[1:]
	}
	if key, ok := language.BestMatch(r.Language, keys); ok {
		return text[key]
	}
	return text["_"]
}
func (r *Renderer) textField(indent, key string, text packageinfo.Text) {
	if text == nil {
		return
	}
	if r.Language != "*" {
		r.field(indent, key, r.Text(text))
		return
	}
	for _, languageKey := range textKeys(text) {
		label := r.styles().key.Render(key) + r.styles().language.Render("["+languageKey+"]") + r.styles().key.Render(":")
		r.printf("%s%s %s\n", indent, label, text[languageKey])
	}
}
func (r *Renderer) none(indent string) { r.printf("%s%s\n", indent, r.styles().muted.Render("(none)")) }

func (r *Renderer) info(v packagemanager.Info) {
	p := v.Package
	if v.File != "" {
		r.section("File")
		path := v.File
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		r.field("  ", "Path", path)
		r.field("  ", "Hash", v.Hash)
		r.printf("\n")
	}
	if v.Installation != nil {
		r.section("Installation")
		path := v.Installation.Directory
		if r.PackagesDir != "" {
			if absolute, err := filepath.Abs(filepath.Join(r.PackagesDir, path)); err == nil {
				path = absolute
			}
		}
		r.field("  ", "Path", path)
		r.field("  ", "Hash", v.Installation.Hash)
		r.field("  ", "InstalledAt", installedAtText(v.Installation.InstalledAt))
		r.printf("\n")
	}
	r.section("Package")
	r.field("  ", "ID", p.ID)
	r.field("  ", "Version", p.Version.String())
	r.textField("  ", "Name", p.Name)
	r.textField("  ", "Description", p.Description)
	r.textField("  ", "Vendor", p.Vendor)
	r.field("  ", "CompatVersion", p.CompatVersion.String())
	r.field("  ", "FormatVersion", p.FormatVersion.String())
	r.field("  ", "RuntimeLevel", fmt.Sprint(p.RuntimeLevel))
	r.textField("  ", "Copyright", p.Copyright)
	r.textField("  ", "Readme", p.Readme)
	if text, ok := packageinfo.TextValue(p.Manifest["license"]); ok {
		r.textField("  ", "License", text)
	}
	if p.URL != nil {
		r.field("  ", "URL", *p.URL)
	}
	r.printf("\n")
	r.section("Dependencies")
	if len(p.Dependencies) == 0 {
		r.none("  ")
	}
	for _, dependency := range p.Dependencies {
		r.printf("  %s=%s\n", dependency.ID, dependency.Version.String())
	}
	r.printf("\n")
	r.section("Contributions")
	if len(p.Contributions) == 0 {
		r.none("  ")
	}
	category := ""
	for _, contribution := range p.Contributions {
		if contribution.Category != category {
			category = contribution.Category
			r.printf("  %s\n", r.styles().key.Render(category+":"))
		}
		label := contribution.ID
		if contribution.Path != "" {
			label += " -> " + contribution.Path
		}
		r.printf("    %s\n", r.styles().identity.Render(label))
		if contribution.SingerDeclaration == nil {
			continue
		}
		r.textField("      ", "Name", contribution.Name)
		if contribution.Interface != "" {
			r.field("      ", "Interface", contribution.Interface)
		}
		if contribution.Level != "" {
			r.field("      ", "Level", contribution.Level.String())
		}
		if contribution.Variant != "" {
			r.field("      ", "Variant", contribution.Variant)
		}
		if !contribution.Expanded {
			r.printf("      %s\n", r.styles().warning.Render("! Metadata expansion unavailable"))
		}
		if contribution.Singer != nil {
			r.textField("      ", "Avatar", contribution.Singer.Avatar)
			r.textField("      ", "Background", contribution.Singer.Background)
			r.textField("      ", "DemoAudio", contribution.Singer.DemoAudio)
		}
	}
}

func (r *Renderer) textTable(headers []string, rows [][]string) {
	style := r.styles()
	t := table.New().Border(lipgloss.NormalBorder()).BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).BorderColumn(false).BorderHeader(true).
		StyleFunc(func(row, _ int) lipgloss.Style {
			cell := style.renderer.NewStyle().PaddingRight(2)
			if row == table.HeaderRow {
				return cell.Bold(true)
			}
			return cell
		}).Headers(headers...).Rows(rows...)
	r.printf("%s\n", t.Render())
}
func (r *Renderer) List(records []packagemanager.InstalledPackage, columns []string) {
	labels := map[string]string{"id": "ID", "version": "Version", "name": "Name", "vendor": "Vendor", "description": "Description", "installed-at": "InstalledAt", "hash": "Hash", "directory": "Directory", "compat-version": "CompatVersion"}
	headers := make([]string, len(columns))
	for i, column := range columns {
		headers[i] = labels[column]
	}
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		cells := map[string]string{"id": record.ID, "version": record.Version.String(), "name": r.Text(record.Name), "vendor": r.Text(record.Vendor), "description": r.Text(record.Description), "installed-at": installedAtText(record.InstalledAt), "hash": record.Hash, "directory": record.Directory, "compat-version": record.CompatVersion.String()}
		row := make([]string, len(columns))
		for i, column := range columns {
			row[i] = strings.ReplaceAll(cells[column], "\t", " ")
		}
		rows = append(rows, row)
	}
	r.textTable(headers, rows)
}

func installedAtText(seconds int64) string {
	return time.Unix(seconds, 0).Local().Format("Mon Jan _2 15:04:05.000 MST 2006")
}

func (r *Renderer) textEvent(event packagemanager.Event) {
	styles := r.styles()
	switch event.Event {
	case "ignored":
		if targets, ok := event.Data.([]string); ok {
			r.section("Ignored missing packages")
			for _, target := range targets {
				r.printf("  %s %s\n", styles.warning.Render("!"), target)
			}
		}
	case "inspect_start":
		if files, ok := event.Data.([]string); ok {
			r.ensureLiveProgress()
			if r.live != nil {
				r.live.start("inspect", files)
				return
			}
			for _, file := range files {
				r.printf("Inspecting %s\n", file)
			}
		}
	case "inspect_ok":
		return // The inspection progress completion resolves the original spinner.
	case "plan":
		if actions, ok := event.Data.([]packagemanager.Action); ok && r.Command == "install" {
			for _, action := range actions {
				if action.Action == "skip" {
					r.printf("%s\n", styles.warning.Render(action.Package.String()+" is already installed with the same hash; skipping"))
				}
				if action.Action == "overwrite" {
					r.printf("%s\n", styles.warning.Render(action.Package.String()+" is already installed with a different hash; will overwrite"))
				}
			}
			if !r.DryRun && r.live != nil {
				targets := []string{}
				for _, action := range actions {
					if action.Action != "skip" {
						targets = append(targets, action.Package.String())
					}
				}
				r.live.start("extract", targets)
			}
		}
		if actions, ok := event.Data.([]packagemanager.Action); ok && r.Command == "remove" {
			for _, group := range []struct{ action, label string }{{"remove", "Packages to remove"}, {"cascade", "Packages to remove by cascade"}} {
				shown := false
				for _, action := range actions {
					if action.Action == group.action {
						if !shown {
							r.section(group.label)
							shown = true
						}
						r.printf("  %s\n", action.Package.String())
					}
				}
			}
		}
		if plan, ok := event.Data.(packagearchive.PackPlan); ok {
			r.packFiles, r.packBytes = len(plan.Files), plan.Bytes
			r.printf("%s %s (%d files, %d bytes)\n", styles.success.Render("✓"), styles.identity.Render(plan.Package.String()), r.packFiles, r.packBytes)
			if r.DryRun {
				r.field("", "Output (dry run)", plan.Output)
			} else if r.live == nil {
				r.printf("Packing: %s\n", plan.Output)
			}
		}
	case "staged":
		return // Keep the complete batch of extraction bars visible until results.
	case "result":
		if action, ok := event.Data.(packagemanager.Action); ok {
			hash := ""
			if action.Hash != "" {
				hash = " [hash=" + action.Hash + "]"
			}
			if r.DryRun {
				r.printf("%s %s [%s]%s\n", styles.warning.Render("[DRY RUN]"), styles.identity.Render(action.Package.String()), action.Action, hash)
				return
			}
			status := map[string]string{"install": "installed", "overwrite": "overwritten", "skip": "skipped", "remove": "removed", "cascade": "removed by cascade"}[action.Action]
			icon := styles.success.Render("✓")
			if action.Action == "skip" {
				icon = styles.warning.Render("!")
			}
			r.printf("%s %s [%s]%s\n", icon, styles.identity.Render(action.Package.String()), status, hash)
		} else if result, ok := event.Data.(map[string]any); ok && r.Command == "pack" {
			identity, _ := result["package"].(packageinfo.Identity)
			r.printf("%s packed %s\n", styles.success.Render("✓"), styles.identity.Render(identity.String()))
			r.field("", "Output", fmt.Sprint(result["file"]))
			r.field("", "Files", fmt.Sprint(r.packFiles))
			if size, ok := result["size"]; ok {
				r.field("", "Size", fmt.Sprintf("%v bytes", size))
			}
			r.field("", "Uncompressed size", fmt.Sprintf("%d bytes", r.packBytes))
			r.field("", "Hash", fmt.Sprint(result["hash"]))
		}
	case "summary":
		if summary, ok := event.Data.(packagemanager.MutationResult); ok {
			label := "Summary"
			if summary.DryRun {
				label += " (dry run)"
			}
			counts := map[string]int{}
			for _, action := range summary.Actions {
				counts[action.Action]++
			}
			if r.Command == "install" {
				r.field("", label, fmt.Sprintf("%d installed, %d overwritten, %d skipped", counts["install"], counts["overwrite"], counts["skip"]))
			} else {
				r.field("", label, fmt.Sprintf("%d removed (%d by cascade), %d ignored", counts["remove"]+counts["cascade"], counts["cascade"], len(summary.Ignored)))
			}
		}
	}
}
