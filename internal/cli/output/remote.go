package output

import (
	"encoding/json"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"strings"
	"unicode"
)

func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(s))
}
func (r *Renderer) remoteInfo(v packagemanager.RemoteInfo) {
	p := v.Package
	r.packageSummary(p.Identity, p.CompatVersion, p.Name, p.Description, p.Vendor)
	r.documentation(v.AssetURLs["license"], v.AssetURLs["readme"], p.URL)
	if p.Deprecated {
		r.field("  ", "Deprecated", "true")
		r.field("  ", "DeprecationReason", p.DeprecationReason)
	}
	r.printf("\n")
	r.dependencies(p.Dependencies)
	r.printf("\n")
	r.section("Singers")
	if len(p.Singers) == 0 {
		r.none("  ")
	}
	for _, s := range p.Singers {
		r.printf("  %s\n", r.styles().identity.Render(s.ID))
		r.textField("    ", "Name", s.Name)
		r.moduleMetadata("    ", s.Interface, s.Level, s.Variant)
	}
	r.printf("\n")
	r.section("Download")
	r.field("  ", "URL", p.DownloadURL)
	r.field("  ", "SHA-512", p.DownloadSha512)
	r.field("  ", "Size", byteCount(p.DownloadSize))
}
func (r *Renderer) remoteEvent(e packagemanager.Event) bool {
	if r.Command == "download" && e.Event == "summary" {
		if summary, ok := e.Data.(map[string]any); ok {
			if r.DryRun {
				r.field("", "Summary (dry run)", "The download plan is complete. No archives were exported.")
			} else {
				actions, _ := summary["actions"].([]packagemanager.Action)
				downloaded, skipped := 0, 0
				for _, action := range actions {
					if action.Action == "skip" {
						skipped++
					} else {
						downloaded++
					}
				}
				r.field("", "Summary", fmt.Sprintf("Archive export is complete (exported: %d, skipped: %d).", downloaded, skipped))
			}
		}
		return true
	}
	if e.Event == "resolution_plan" {
		if plan, ok := e.Data.(packagemanager.ResolutionPlan); ok {
			r.remoteInstall = true
			r.remoteInstallPlan(plan)
		}
		return true
	}
	if e.Event == "plan_updated" {
		r.printf("Installed package state changed. Preparation requires a new plan.\n")
		return true
	}
	if e.Event == "summary" {
		if summary, ok := e.Data.(map[string]any); ok && r.Command == "install" {
			if _, ok := summary["verification"]; ok {
				pending, _ := summary["pendingArchiveChecks"].([]packageinfo.Identity)
				r.field("", "Summary (dry run)", fmt.Sprintf("Archive validation is pending (archives: %d). No packages were installed.", len(pending)))
			} else {
				actions, _ := summary["actions"].([]packagemanager.Action)
				r.installSummary(actions, false)
			}
			return true
		}
	}
	if r.Command == "repo create" || r.Command == "cache clean" {
		if e.Event == "summary" {
			raw, err := json.MarshalIndent(e.Data, "", "  ")
			if err != nil {
				r.writeError = err
			} else {
				r.printf("%s\n", raw)
			}
		}
		return true
	}
	return false
}

func byteCount(n int64) string {
	units := []string{"bytes", "KiB", "MiB", "GiB", "TiB"}
	value := float64(n)
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d bytes", n)
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}
