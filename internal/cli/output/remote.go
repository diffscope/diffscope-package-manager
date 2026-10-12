package output

import (
	"encoding/json"
	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
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
	r.field("  ", "Size", "unknown")
}
func (r *Renderer) remoteEvent(e packagemanager.Event) bool {
	if e.Event == "resolution_plan" {
		if plan, ok := e.Data.(packagemanager.ResolutionPlan); ok {
			r.printf("Preparation includes %d package candidates and %d archives requiring verification. Network policy: %s.\n", len(plan.Packages), len(plan.PendingArchiveChecks), plan.NetworkPolicy)
		}
		return true
	}
	if e.Event == "plan_updated" {
		r.printf("Installed package state changed. Preparation requires a new plan.\n")
		return true
	}
	if e.Event == "summary" {
		if summary, ok := e.Data.(map[string]any); ok && r.Command == "install" {
			if verification, ok := summary["verification"]; ok {
				r.printf("The dry run is complete with verification scope %v. Archive checks remain pending.\n", verification)
			} else {
				r.printf("Summary: commit %v\n", summary["commitOutcome"])
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
