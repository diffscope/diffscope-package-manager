package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/download"
	"github.com/diffscope/diffscope-package-manager/internal/netpolicy"
	"io"
	"regexp"
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
	ProgressMode        string
	downloadProgressAt  map[string]time.Time
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
		// Redact presentation copies only. Internal manifests and cache keys keep
		// their original bytes and exact URLs for verification and requests.
		b, err := json.Marshal(v)
		if err != nil {
			r.writeError = err
			return
		}
		var presentation any
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.UseNumber()
		if err = decoder.Decode(&presentation); err != nil {
			r.writeError = err
			return
		}
		r.writeError = json.NewEncoder(r.Out).Encode(redactValues(presentation))
	}
}

var outputURL = regexp.MustCompile(`https?://[^\s"<>]+`)

func redactValues(v any) any {
	switch x := v.(type) {
	case string:
		return outputURL.ReplaceAllStringFunc(x, netpolicy.Redact)
	case map[string]any:
		for k, value := range x {
			x[k] = redactValues(value)
		}
	case []any:
		for i, value := range x {
			x[i] = redactValues(value)
		}
	}
	return v
}
func (r *Renderer) Observer(e packagemanager.Event) {
	if e.Event == "progress" {
		if p, ok := e.Data.(download.Progress); ok {
			r.DownloadProgress(p)
			return
		}
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
		fmt.Fprintln(r.Err, r.errorStyles().warning.Render("!")+" "+outputURL.ReplaceAllStringFunc(safeText(d), netpolicy.Redact))
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
			result := map[string]any{"command": r.Command, "event": "error", "error": payload, "diagnostics": r.diagnostics}
			if e != nil && e.DiagnosticDetails != nil {
				result["diagnosticDetails"] = e.DiagnosticDetails
			}
			r.write(result)
		} else {
			r.write(map[string]any{"ok": false, "command": r.Command, "error": payload, "diagnostics": r.diagnostics})
		}
	} else {
		fmt.Fprintln(r.Err, r.errorStyles().failure.Render("✗ Error: "+outputURL.ReplaceAllStringFunc(safeText(err.Error()), netpolicy.Redact)))
	}
	return &ReportedError{err}
}
func (r *Renderer) Query(data any, diagnostics []string) error {
	if r.JSON {
		result := map[string]any{"ok": true, "command": r.Command, "data": data, "diagnostics": diagnostics}
		if info, ok := data.(packagemanager.RemoteInfo); ok {
			result["diagnosticDetails"] = info.DiagnosticDetails
		}
		r.write(result)
	} else {
		r.Diagnostics(diagnostics)
		switch v := data.(type) {
		case packagemanager.Info:
			r.info(v)
		case packagemanager.RemoteInfo:
			r.remoteInfo(v)
		case []packagemanager.InstalledPackage:
			r.List(v, []string{"id", "version"})
		default:
			b, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				_, err = fmt.Fprintln(r.Out, string(b))
			}
			r.writeError = err
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
			return nil, fmt.Errorf("The list column %q is unknown.", c)
		}
		columns[i] = c
	}
	return columns, nil
}
func (r *Renderer) WriteError() error {
	if r.writeError == nil && r.live != nil {
		return r.live.outputError()
	}
	return r.writeError
}
func (r *Renderer) DownloadProgress(p download.Progress) {
	if r.JSON {
		r.write(map[string]any{"command": r.Command, "event": "progress", "data": p})
		return
	}
	if r.ProgressMode == "none" {
		return
	}
	r.ensureLiveProgress()
	if r.live != nil {
		r.live.download(p)
		return
	}
	if r.downloadProgressAt == nil {
		r.downloadProgressAt = map[string]time.Time{}
	}
	if p.State == "update" && time.Since(r.downloadProgressAt[p.TaskID]) < 2*time.Second {
		return
	}
	r.downloadProgressAt[p.TaskID] = time.Now()
	total := "unknown"
	if p.TotalKnown {
		total = fmt.Sprintf("%d", p.Total)
	}
	line := fmt.Sprintf("%s %s %d/%s bytes %.1f KiB/s [%s]", p.Target, p.Status, p.Current, total, p.BytesPerSecond/1024, p.CacheStatus)
	if p.State == "source-switch" {
		line += " source " + p.PreviousSourceID[:min(8, len(p.PreviousSourceID))] + " -> " + p.SourceID[:min(8, len(p.SourceID))]
	}
	_, err := fmt.Fprintln(r.Err, line)
	if err != nil && r.writeError == nil {
		r.writeError = err
	}
}
