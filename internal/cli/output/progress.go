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

// Renders mutation progress with bounded NDJSON updates.
// Boundary events, file changes, and final byte counts are retained. Queries return one JSON value.
func (r *Renderer) Progress(p packagemanager.Progress) {
	if !r.JSON && r.ProgressMode == "none" {
		return
	}
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
	if r.JSON || r.progressInitialized || (r.ProgressMode != "" && r.ProgressMode != "auto") {
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
		r.live.height = func() int {
			_, height, err := term.GetSize(file.Fd())
			if err != nil {
				return 16
			}
			return max(4, height-3)
		}
	}
}

// Restores terminal state and stops animation, including during panic unwinding.
func (r *Renderer) Close() {
	r.stopProgress()
	for _, restore := range r.restoreTerminal {
		_ = restore()
	}
	r.restoreTerminal = nil
}

// Enables ANSI processing on native Windows consoles and saves modes for restoration.
// JSON and redirected streams require no console changes.
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
