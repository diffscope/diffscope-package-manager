package output

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/muesli/termenv"
	"github.com/pterm/pterm"
)

// Keep the original PTerm formatter and settings. UpdateTitle formats one frame
// without Start's process-global printer registration or animation goroutines.
func originalProgressBar(width, current int) string {
	var frame bytes.Buffer
	bar := pterm.DefaultProgressbar.WithWriter(&frame).WithTotal(1000).
		WithMaxWidth(max(8, width)).WithShowElapsedTime(false).
		WithShowCount(false).WithShowTitle(false)
	bar.Current = min(1000, max(0, current))
	bar.IsActive = true
	bar.UpdateTitle("")
	return strings.TrimPrefix(frame.String(), "\r")
}

type liveRow struct {
	target, file string
	current      int
	done, failed bool
}

// All rows of a batch stay visible together, as with the original MultiPrinter.
// One owner serializes refreshes, updates, cursor cleanup and ordinary output.
type liveProgress struct {
	mu                sync.Mutex
	out               io.Writer
	width             func() int
	styles            *textStyles
	stop, stopped     chan struct{}
	rows              []liveRow
	mode              string
	inspectIndex      int
	active, closed    bool
	drawnLines        int
	started, lastDraw time.Time
	err               error
}

func newLiveProgress(out io.Writer, width func() int, styles *textStyles) *liveProgress {
	l := &liveProgress{out: out, width: width, styles: styles, stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(l.stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-ticker.C:
				l.mu.Lock()
				if l.active && !l.closed {
					l.draw()
				}
				l.mu.Unlock()
			}
		}
	}()
	return l
}

func (l *liveProgress) start(mode string, targets []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.freeze()
	l.mode, l.rows, l.drawnLines = mode, nil, 0
	l.inspectIndex = -1
	for _, target := range targets {
		l.rows = append(l.rows, liveRow{target: target})
	}
	if len(l.rows) == 0 {
		return
	}
	l.active = true
	l.started = time.Now()
	l.write("\x1b[?25l")
	l.draw()
}

func (l *liveProgress) frame() []string {
	width := max(1, l.width())
	lines := []string{}
	for _, row := range l.rows {
		if l.mode == "inspect" {
			text := "Inspecting " + row.target
			sequence := pterm.DefaultSpinner.Sequence
			index := int(time.Since(l.started)/pterm.DefaultSpinner.Delay) % len(sequence)
			icon := l.styles.renderer.NewStyle().Foreground(lipgloss.Color("14")).Render(sequence[index])
			if row.done {
				icon = l.styles.success.Render("✓")
			}
			if row.failed {
				icon = l.styles.failure.Render("✗")
				text = l.styles.failure.Render(text)
			}
			if !row.done && !row.failed {
				text = l.styles.renderer.NewStyle().Foreground(lipgloss.Color("15")).Render(text)
			}
			lines = append(lines, strings.Split(ansi.Hardwrap(icon+" "+text, width, true), "\n")...)
			continue
		}
		verb := "extracting"
		if l.mode == "pack" {
			verb = "packing"
		}
		title := l.styles.identity.Render(row.target) + ": " + verb
		if row.file != "" {
			title += " " + row.file
		}
		lines = append(lines, strings.Split(ansi.Hardwrap(title, width, true), "\n")...)
		bar := originalProgressBar(width, row.current)
		if l.styles.renderer.ColorProfile() == termenv.Ascii {
			bar = ansi.Strip(bar)
		}
		// PTerm needs at least eight columns. Very narrow windows retain the
		// percentage, rather than wrapping a progress bar onto unrelated rows.
		if width < 8 {
			bar = ansi.Truncate(fmt.Sprintf("%3d%%", row.current/10), width, "")
		}
		lines = append(lines, bar)
	}
	return lines
}

func (l *liveProgress) write(s string) {
	if l.err == nil {
		_, l.err = io.WriteString(l.out, s)
	}
}
func (l *liveProgress) draw() {
	lines := l.frame()
	var screen strings.Builder
	if l.drawnLines > 0 {
		fmt.Fprintf(&screen, "\x1b[%dF", l.drawnLines)
	}
	for _, line := range lines {
		screen.WriteString("\x1b[2K")
		screen.WriteString(line)
		screen.WriteString("\r\n")
	}
	for i := len(lines); i < l.drawnLines; i++ {
		screen.WriteString("\x1b[2K\r\n")
	}
	if excess := l.drawnLines - len(lines); excess > 0 {
		fmt.Fprintf(&screen, "\x1b[%dF", excess)
	}
	l.write(screen.String())
	l.drawnLines = len(lines)
	l.lastDraw = time.Now()
}

func (l *liveProgress) update(p packagemanager.Progress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if !l.active {
		l.mode = p.Phase
		l.rows = []liveRow{{target: p.Target}}
		l.drawnLines = 0
		l.active = true
		l.started = time.Now()
		l.write("\x1b[?25l")
	}
	if p.Phase == "inspect" && p.State == "start" {
		for i, row := range l.rows {
			if row.target == p.Target && !row.done && !row.failed {
				l.inspectIndex = i
				break
			}
		}
	}
	for i := range l.rows {
		row := &l.rows[i]
		if row.target != p.Target || (p.Phase == "inspect" && i != l.inspectIndex) {
			continue
		}
		row.file = p.FilePath
		if p.Total > 0 {
			row.current = int(min(int64(1000), p.Current*1000/p.Total))
		}
		if p.State == "done" {
			row.done = true
			if p.Phase != "inspect" {
				row.current = 1000
			}
		}
		break
	}
	if p.State == "start" || p.State == "done" || time.Since(l.lastDraw) >= 100*time.Millisecond {
		l.draw()
	}
}

func (l *liveProgress) failInspection(target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active || l.mode != "inspect" {
		return
	}
	for i := range l.rows {
		if i == l.inspectIndex && l.rows[i].target == target {
			l.rows[i].failed = true
		}
	}
}
func (l *liveProgress) freeze() {
	if !l.active {
		return
	}
	l.draw()
	// Always attempt restoration, including after a broken output stream.
	_, err := io.WriteString(l.out, "\x1b[?25h")
	if l.err == nil {
		l.err = err
	}
	l.active = false
	l.drawnLines = 0
}
func (l *liveProgress) pause() { l.mu.Lock(); defer l.mu.Unlock(); l.freeze() }
func (l *liveProgress) close() error {
	l.mu.Lock()
	if !l.closed {
		l.freeze()
		l.closed = true
		close(l.stop)
	}
	l.mu.Unlock()
	<-l.stopped
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}
