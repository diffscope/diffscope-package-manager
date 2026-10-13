package output

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/download"
	"github.com/muesli/termenv"
	"github.com/pterm/pterm"
)

func (l *liveProgress) download(p download.Progress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if l.mode != "download" {
		l.freeze()
		l.mode, l.rows, l.drawnLines = "download", nil, 0
		l.started = time.Now()
	}
	if !l.active {
		l.active = true
		l.write("\x1b[?25l")
	}
	index := -1
	for i, row := range l.rows {
		if row.target == p.TaskID {
			index = i
			break
		}
	}
	if index < 0 {
		l.rows = append(l.rows, liveRow{target: p.TaskID})
		index = len(l.rows) - 1
	}
	l.rows[index].download = &p
	if p.State == "done" || p.State == "failed" || p.State == "cancelled" {
		// Terminal records precede the live frame so subsequent refreshes retain them.
		if l.drawnLines > 0 {
			l.write(fmt.Sprintf("\x1b[%dF\x1b[J", l.drawnLines))
			l.drawnLines = 0
		}
		width := max(1, l.width())
		status := ansi.Hardwrap(downloadStatus(&p), width, true)
		l.write(strings.ReplaceAll(status, "\n", "\r\n") + "\r\n")
		l.write(ansi.Truncate(l.downloadIndicator(&p, width), width, "") + "\r\n")
	}
	if p.State != "update" || time.Since(l.lastDraw) >= 100*time.Millisecond {
		l.draw()
	}
}

func (l *liveProgress) downloadFrame(width int) []string {
	limit := 16
	if l.height != nil {
		limit = l.height()
	}
	limit = max(1, limit)
	done, failed := 0, 0
	active := []*download.Progress{}
	for _, row := range l.rows {
		p := row.download
		if p == nil {
			continue
		}
		switch p.State {
		case "done":
			done++
		case "failed", "cancelled":
			failed++
		default:
			active = append(active, p)
		}
	}
	lines := []string{fmt.Sprintf("Downloads: %d/%d completed, %d failed.", done, len(l.rows), failed)}
	// Each task requires a status line and a progress indicator. Reserve a
	// summary line for hidden tasks if the terminal cannot display every task.
	visible := min(len(active), (limit-1)/2)
	if visible < len(active) {
		visible = max(0, (limit-2)/2)
	}
	for _, p := range active[:visible] {
		lines = append(lines, downloadStatus(p))
		lines = append(lines, l.downloadIndicator(p, width))
	}
	if visible < len(active) && len(lines) < limit {
		lines = append(lines, fmt.Sprintf("Additional active downloads: %d.", len(active)-visible))
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lines
}

func downloadStatus(p *download.Progress) string {
	total, eta := "unknown MiB", ""
	if p.TotalKnown {
		total = fmt.Sprintf("%.1f MiB", float64(p.Total)/(1<<20))
	}
	if p.ETASeconds != nil && p.State != "done" {
		eta = fmt.Sprintf(" ETA %.0fs", *p.ETASeconds)
	}
	line := fmt.Sprintf("%s  %s  %.1f/%s  %.1f KiB/s%s [%s]", p.Target, p.Status, float64(p.Current)/(1<<20), total, p.BytesPerSecond/1024, eta, p.CacheStatus)
	if p.State == "retry" {
		line += fmt.Sprintf(" retry in %dms", p.RetryInMs)
	}
	return safeText(line)
}

func (l *liveProgress) downloadIndicator(p *download.Progress, width int) string {
	if p.State != "done" && (!p.TotalKnown || p.Total <= 0) {
		sequence := pterm.DefaultSpinner.Sequence
		index := int(time.Since(l.started)/pterm.DefaultSpinner.Delay) % len(sequence)
		return sequence[index] + " Total size unknown."
	}
	current := 1000
	if p.State != "done" {
		current = int(min(1, max(0, float64(p.Current)/float64(p.Total))) * 1000)
	}
	if width < 8 {
		return fmt.Sprintf("%3d%%", current/10)
	}
	if l.styles.renderer.ColorProfile() == termenv.Ascii {
		percentage := fmt.Sprintf(" %3d%%", current/10)
		cells := width - len(percentage)
		filled := cells * current / 1000
		return strings.Repeat("█", filled) + strings.Repeat("░", cells-filled) + percentage
	}
	return originalProgressBar(width, current)
}
