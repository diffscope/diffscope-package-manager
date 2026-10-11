package output

import (
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/muesli/termenv"
)

// Progress preserves a bounded NDJSON update rate while always delivering
// boundaries, file changes and final byte counts. Query output remains one JSON value.
func (r *Renderer) Progress(p packagemanager.Progress) {
	now := time.Now()
	previous := r.lastProgress
	if r.JSON {
		if p.State != "update" || previous.State != "update" || previous.FilePath != p.FilePath ||
			previous.FilesDone != p.FilesDone || now.Sub(r.progressAt) >= 100*time.Millisecond ||
			(p.Current == p.Total && previous.Current != p.Current) {
			r.write(map[string]any{"command": r.Command, "event": "progress", "data": p})
			r.progressAt = now
		}
		r.lastProgress = p
		return
	}
	r.ensureLiveProgress()
	switch p.Phase {
	case "inspect":
		if r.live != nil {
			r.live.update(p)
		} else if p.State == "done" {
			r.printf("%s Inspecting %s\n", r.styles().success.Render("✓"), p.Target)
		}
	case "extract", "pack":
		if r.live != nil {
			r.live.update(p)
		} else if p.State == "update" && p.FilePath != "" && (p.FilePath != previous.FilePath || p.FilesDone != previous.FilesDone) {
			verb := "extracting"
			if p.Phase == "pack" {
				verb = "packing"
			}
			r.printf("%s %s (%d/%d)\n", verb, p.FilePath, p.Current, p.Total)
		}
	case "check":
		if p.State == "start" {
			path, err := filepath.Abs(p.Target)
			if err != nil {
				path = p.Target
			}
			r.printf("Checking package directory: %s\n", path)
		}
	case "resolve":
		if p.State == "start" {
			r.pauseProgress()
		}
	}
	r.lastProgress = p
}

func (r *Renderer) ensureLiveProgress() {
	if r.JSON || r.progressInitialized {
		return
	}
	r.progressInitialized = true
	if file, ok := r.Out.(*os.File); ok && term.IsTerminal(file.Fd()) && os.Getenv("TERM") != "dumb" {
		r.live = newLiveProgress(r.Out, func() int {
			width, _, err := term.GetSize(file.Fd())
			if err != nil {
				return 80
			}
			return width
		}, r.styles())
	}
}

// Close also runs during panic unwinding. No animation or hidden cursor survives
// an error, cancellation, or command return.
func (r *Renderer) Close() {
	r.stopProgress()
	for _, restore := range r.restoreTerminal {
		_ = restore()
	}
	r.restoreTerminal = nil
}

// EnableTerminal enables ANSI processing on native Windows consoles and saves
// their modes for restoration. It is a no-op for JSON and redirected streams.
func (r *Renderer) EnableTerminal() {
	if r.JSON {
		return
	}
	for _, destination := range []io.Writer{r.Out, r.Err} {
		if destination == nil {
			continue
		}
		restore, err := termenv.EnableVirtualTerminalProcessing(termenv.NewOutput(destination))
		if err == nil {
			r.restoreTerminal = append(r.restoreTerminal, restore)
		}
	}
}

func (r *Renderer) stopProgress() {
	if r.live != nil {
		if err := r.live.close(); r.writeError == nil {
			r.writeError = err
		}
		r.live = nil
	}
}
func (r *Renderer) pauseProgress() {
	if r.live != nil {
		r.live.pause()
	}
}
