package output

import (
	"fmt"
	"sort"
	"strings"

	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
)

func (r *Renderer) installPlanTitle() {
	title := "Installation plan"
	if r.Command == "download" {
		title = "Download plan"
	}
	if r.DryRun {
		title += " (dry run)"
	}
	r.section(title)
}

func (r *Renderer) localInstallPlan(actions []packagemanager.Action) {
	items := make([]installPlanItem, 0, len(actions))
	for _, action := range actions {
		items = append(items, installPlanItem{identity: action.Package.String(), action: action.Action})
	}
	r.installPlan(items)
}

func (r *Renderer) remoteInstallPlan(plan packagemanager.ResolutionPlan) {
	providers := map[string][]string{}
	for _, edge := range plan.EffectiveEdges {
		requester := edge.Requester.String()
		providers[requester] = append(providers[requester], edge.Provider.String())
	}
	requested := map[string]bool{}
	selected := map[string]bool{}
	queue := []string{}
	for _, root := range plan.Roots {
		key := root.String()
		requested[key], selected[key] = true, true
		queue = append(queue, key)
	}
	for _, p := range plan.Packages {
		if p.Action != "keep" && !selected[p.String()] {
			selected[p.String()] = true
			queue = append(queue, p.String())
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, provider := range providers[queue[i]] {
			if !selected[provider] {
				selected[provider] = true
				queue = append(queue, provider)
			}
		}
	}
	items := []installPlanItem{}
	for _, p := range plan.Packages {
		key := p.String()
		if !selected[key] {
			continue
		}
		items = append(items, installPlanItem{identity: key, action: p.Action, archiveSource: p.ArchiveSource, dependency: !requested[key]})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].dependency != items[j].dependency {
			return !items[i].dependency
		}
		return items[i].identity < items[j].identity
	})
	var transferred, cached int64
	for _, p := range plan.Packages {
		if !selected[p.String()] {
			continue
		}
		if p.ArchiveSource == "download" {
			transferred += p.DownloadSize
		} else if p.ArchiveSource == "cache" || p.ArchiveSource == "prepared" {
			cached += p.DownloadSize
		}
	}
	r.installPlanWithSize(items, transferred, cached)
}

type installPlanItem struct {
	identity, action, archiveSource string
	dependency                      bool
}

func (r *Renderer) installPlan(items []installPlanItem) {
	r.installPlanWithSize(items, 0, 0)
}
func (r *Renderer) installPlanWithSize(items []installPlanItem, transferred, cached int64) {
	r.installPlanTitle()
	for _, group := range []struct{ action, title string }{
		{"install", "Installations"},
		{"download", "Archives"},
		{"overwrite", "Overwrites"},
		{"check", "Archive checks"},
		{"skip", "Skips"},
		{"keep", "Installed dependencies"},
	} {
		shown := false
		for _, item := range items {
			if item.action != group.action {
				continue
			}
			if !shown {
				r.printf("  %s\n", r.styles().key.Render(group.title+":"))
				shown = true
			}
			labels := []string{}
			if item.archiveSource != "" && item.archiveSource != "none" {
				label := item.archiveSource
				if label == "local" {
					label = "local file"
				}
				labels = append(labels, label)
			}
			if item.dependency && item.action != "keep" {
				labels = append(labels, "dependency")
			}
			suffix := ""
			if len(labels) > 0 {
				suffix = " " + r.styles().muted.Render("["+strings.Join(labels, ", ")+"]")
			}
			r.printf("    %s%s\n", r.styles().identity.Render(item.identity), suffix)
		}
	}
	if transferred > 0 || cached > 0 {
		r.printf("  Download size: %s. Cached archives: %s.\n", byteCount(transferred), byteCount(cached))
	}
	if r.Command != "download" {
		r.printf("\nInstalling a package constitutes acceptance of its license terms. Use of generative artificial intelligence and other technologies must comply with applicable laws and regulations in the relevant jurisdiction. Users are responsible for fulfilling all compliance obligations arising from their use of installed packages.\n\n")
	} else {
		r.printf("\n")
	}
}

func (r *Renderer) installSummary(actions []packagemanager.Action, dryRun bool) {
	counts := map[string]int{}
	for _, action := range actions {
		counts[action.Action]++
	}
	if dryRun {
		r.field("", "Summary (dry run)", fmt.Sprintf("The plan is complete (installations: %d, overwrites: %d, skips: %d).", counts["install"], counts["overwrite"], counts["skip"]))
		return
	}
	r.field("", "Summary", fmt.Sprintf("Installation is complete (installed: %d, overwritten: %d, skipped: %d).", counts["install"], counts["overwrite"], counts["skip"]))
}
