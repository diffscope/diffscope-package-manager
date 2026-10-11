package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
)

func progressSample(state string, current int64) packagemanager.Progress {
	return packagemanager.Progress{Phase: "extract", Target: "vendor/voice=1.0.0.0", State: state, Progress: packagearchive.Progress{FilePath: "音源/resource.bin", Current: current, Total: 1000, FilesTotal: 1}}
}
func TestLiveProgressAndCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var out, errOut bytes.Buffer
		r := &Renderer{Command: "install", Out: &out, Err: &errOut, progressInitialized: true}
		r.live = newLiveProgress(&out, func() int { return 60 }, newTextStyles(&out))
		r.Progress(progressSample("start", 0))
		r.Progress(progressSample("update", 250))
		time.Sleep(120 * time.Millisecond)
		if fail {
			_ = r.Finish(context.Canceled)
		} else {
			r.Progress(progressSample("done", 1000))
			r.Close()
		}
		s := out.String()
		if !strings.Contains(s, "25%") || !strings.Contains(s, "\x1b[?25l") || !strings.HasSuffix(s, "\x1b[?25h") {
			t.Fatal("missing bar or cursor restoration", s)
		}
		if fail && strings.Contains(s, "100%") {
			t.Fatal("failure forced 100%", s)
		}
		if fail && !strings.Contains(errOut.String(), "canceled") {
			t.Fatal(errOut.String())
		}
		if !strings.Contains(s, "█") || strings.Contains(s, "[===") {
			t.Fatal("original block bar was replaced", s)
		}
		for _, line := range strings.Split(ansi.Strip(s), "\n") {
			if ansi.StringWidth(strings.TrimSpace(line)) > 60 {
				t.Fatal("terminal line wraps", line)
			}
		}
	}
}
func TestProgressJSONAndRedirectedOutput(t *testing.T) {
	for _, structured := range []bool{false, true} {
		var out, errOut bytes.Buffer
		r := &Renderer{Command: "install", JSON: structured, Mutation: true, Out: &out, Err: &errOut}
		r.Progress(progressSample("start", 0))
		for n := int64(1); n <= 1000; n++ {
			r.Progress(progressSample("update", n))
		}
		r.Progress(progressSample("done", 1000))
		if err := r.Finish(nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "\x1b") || errOut.Len() != 0 {
			t.Fatal("terminal control leaked", out.String(), errOut.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) > 10 {
			t.Fatal("unbounded progress output", len(lines))
		}
		if structured {
			for _, line := range lines {
				var e struct {
					Event string
					Data  packagemanager.Progress
				}
				if err := json.Unmarshal([]byte(line), &e); err != nil || e.Event != "progress" {
					t.Fatal(line, err)
				}
			}
			if !strings.Contains(lines[len(lines)-1], `"state":"done"`) {
				t.Fatal("lost final progress")
			}
		}
	}
}

type failingWriter struct {
	writes    int
	recovered bool
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if strings.Contains(string(p), "\x1b[?25h") {
		w.recovered = true
	}
	return 0, io.ErrClosedPipe
}
func TestProgressOutputFailureRestoresCursor(t *testing.T) {
	w := &failingWriter{}
	r := &Renderer{Out: w, Err: io.Discard, progressInitialized: true}
	r.live = newLiveProgress(w, func() int { return 80 }, newTextStyles(w))
	r.Progress(progressSample("start", 0))
	if err := r.Finish(nil); !errors.Is(err, io.ErrClosedPipe) || !w.recovered {
		t.Fatal("write error or cleanup lost", err, w)
	}
}
