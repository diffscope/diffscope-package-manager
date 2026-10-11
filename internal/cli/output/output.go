package output

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
)

type ReportedError struct{ error }

func (e *ReportedError) Unwrap() error { return e.error }

type Renderer struct {
	Command             string
	JSON                bool
	Language            string
	PackagesDir         string
	DryRun              bool
	Out, Err            io.Writer
	Mutation            bool
	writeError          error
	diagnostics         []string
	style               *textStyles
	errStyle            *textStyles
	packFiles           int
	packBytes           int64
	live                *liveProgress
	progressInitialized bool
	lastProgress        packagemanager.Progress
	progressAt          time.Time
	restoreTerminal     []func() error
}

func (r *Renderer) write(v any) {
	if r.writeError == nil {
		r.writeError = json.NewEncoder(r.Out).Encode(v)
	}
}
func (r *Renderer) Observer(e packagemanager.Event) {
	if e.Event == "progress" {
		if p, ok := e.Data.(packagemanager.Progress); ok {
			r.Progress(p)
		}
		return
	}
	if r.JSON {
		r.write(map[string]any{"command": r.Command, "event": e.Event, "data": e.Data})
		return
	}
	switch e.Event {
	case "inspect_start", "inspect_ok", "staged":
	default:
		r.pauseProgress()
	}
	r.textEvent(e)
}
func (r *Renderer) Diagnostics(diagnostics []string) {
	r.diagnostics = diagnostics
	if r.JSON {
		return
	}
	if len(diagnostics) > 0 {
		r.pauseProgress()
	}
	for _, d := range diagnostics {
		fmt.Fprintln(r.Err, r.errorStyles().warning.Render("!")+" "+d)
	}
}
func (r *Renderer) Finish(err error) error {
	if err != nil && r.live != nil && r.lastProgress.Phase == "inspect" && r.lastProgress.State != "done" {
		r.live.failInspection(r.lastProgress.Target)
	}
	r.stopProgress()
	defer r.Close()
	if err == nil {
		err = r.writeError
	}
	if err == nil {
		return nil
	}
	code := "IO_ERROR"
	details := any(nil)
	var e *packagemanager.Error
	if errors.As(err, &e) {
		code = e.Code
		details = e.Details
	}
	if errors.Is(err, context.Canceled) {
		code = "CANCELLED"
	}
	payload := map[string]any{"code": code, "message": err.Error()}
	if details != nil {
		payload["details"] = details
	}
	if r.JSON {
		if r.Mutation {
			r.write(map[string]any{"command": r.Command, "event": "error", "error": payload, "diagnostics": r.diagnostics})
		} else {
			r.write(map[string]any{"ok": false, "command": r.Command, "error": payload, "diagnostics": r.diagnostics})
		}
	} else {
		fmt.Fprintln(r.Err, r.errorStyles().failure.Render("✗ Error: "+err.Error()))
	}
	return &ReportedError{err}
}
func (r *Renderer) Query(data any, diagnostics []string) error {
	if r.JSON {
		r.write(map[string]any{"ok": true, "command": r.Command, "data": data, "diagnostics": diagnostics})
	} else {
		r.Diagnostics(diagnostics)
		switch v := data.(type) {
		case packagemanager.Info:
			r.info(v)
		case []packagemanager.InstalledPackage:
			r.List(v, []string{"id", "version"})
		default:
			r.writeError = fmt.Errorf("unsupported output")
		}
	}
	return r.Finish(nil)
}
func ParseColumns(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		s = "id,version"
	}
	columns := strings.Split(s, ",")
	allowed := map[string]bool{"id": true, "version": true, "name": true, "vendor": true, "description": true, "installed-at": true, "hash": true, "directory": true, "compat-version": true}
	for i, c := range columns {
		c = strings.ToLower(strings.TrimSpace(c))
		switch c {
		case "installed_at", "installedat":
			c = "installed-at"
		case "compat_version", "compatversion":
			c = "compat-version"
		}
		if !allowed[c] {
			return nil, fmt.Errorf("unknown list column %q", c)
		}
		columns[i] = c
	}
	return columns, nil
}
