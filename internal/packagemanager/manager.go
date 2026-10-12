package packagemanager

import (
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
)

type Error struct {
	Code              string
	Message           string
	Details           any
	Cause             error
	DiagnosticDetails []Diagnostic
}

type Diagnostic struct {
	Code     string                `json:"code"`
	Severity string                `json:"severity"`
	Identity *packageinfo.Identity `json:"identity,omitempty"`
	SourceID string                `json:"sourceId,omitempty"`
	Message  string                `json:"message"`
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }
func failure(code string, err error) error {
	return &Error{Code: code, Message: err.Error(), Cause: err}
}

type Event struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}
type Observer func(Event)

func emit(observer Observer, event string, data any) {
	if observer != nil {
		observer(Event{event, data})
	}
}

type Manager struct {
	Root   string
	Limits packagearchive.Limits
}

func New(root string) *Manager { return &Manager{Root: root, Limits: packagearchive.DefaultLimits()} }

type Action struct {
	Package packageinfo.Identity `json:"package"`
	Action  string               `json:"action"`
	File    string               `json:"file,omitempty"`
	Hash    string               `json:"hash,omitempty"`
}
type MutationResult struct {
	Actions     []Action `json:"actions"`
	DryRun      bool     `json:"dryRun"`
	Diagnostics []string `json:"diagnostics,omitempty"`
	Ignored     []string `json:"ignored,omitempty"`
}
