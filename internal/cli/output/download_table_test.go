package output

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/download"
	"github.com/muesli/termenv"
	"github.com/pterm/pterm"
)

func TestDownloadTableFoldsDoneAndFitsTerminal(t *testing.T) {
	var out bytes.Buffer
	live := newLiveProgress(&out, func() int { return 55 }, newTextStyles(&out))
	live.height = func() int { return 4 }
	for i, id := range []string{"alpha", "beta", "gamma", "delta"} {
		state := "update"
		if i == 0 {
			state = "done"
		}
		live.download(download.Progress{TaskID: id, Target: id, State: state, Status: "downloading", Current: 1024, CacheStatus: "resume"})
	}
	live.mu.Lock()
	frame := live.frame()
	live.mu.Unlock()
	live.close()
	if len(frame) > 4 || !strings.Contains(strings.Join(frame, "\n"), "Additional active downloads:") {
		t.Fatal(frame)
	}
	if !strings.HasSuffix(out.String(), "\x1b[?25h") {
		t.Fatal("cursor not restored")
	}
}

func TestDownloadBarsAndUnknownSize(t *testing.T) {
	known := &download.Progress{TaskID: "known", Target: "known=1.0.0.0", State: "update", Status: "downloading", Current: 1 << 20, Total: 4 << 20, TotalKnown: true, BytesPerSecond: 256 << 10, CacheStatus: "resume"}
	unknown := &download.Progress{TaskID: "unknown", Target: "unknown=1.0.0.0", State: "update", Status: "downloading", Current: 2 << 20, CacheStatus: "miss"}
	live := &liveProgress{width: func() int { return 100 }, styles: newTextStyles(io.Discard), mode: "download", started: time.Now(), rows: []liveRow{{download: known}, {download: unknown}}}
	live.styles.renderer.SetColorProfile(termenv.Ascii)
	frame := live.frame()
	if len(frame) != 5 || !strings.Contains(frame[1], "1.0/4.0 MiB") || !strings.Contains(frame[1], "256.0 KiB/s") || !strings.Contains(frame[2], "25%") || !strings.Contains(frame[2], "█") || !strings.Contains(frame[2], "░") {
		t.Fatal("The known-length download lacks byte counts or a progress bar.", frame)
	}
	if !strings.Contains(frame[3], "2.0/unknown") || !strings.Contains(frame[4], "Total size unknown.") || strings.Contains(frame[4], "%") {
		t.Fatal("The unknown-length download lacks an indeterminate indicator.", frame)
	}
	known.Current = 3 << 20
	if frame = live.frame(); !strings.Contains(frame[2], "75%") {
		t.Fatal("The download progress bar did not advance.", frame)
	}
	unknown.TotalKnown, unknown.Total = true, 8<<20
	if frame = live.frame(); !strings.Contains(frame[4], "25%") || !strings.Contains(frame[2], "75%") {
		t.Fatal("Concurrent download progress bars interfered with each other.", frame)
	}
	known.State = "done"
	if frame = live.frame(); len(frame) != 3 || !strings.Contains(frame[0], "1/2 completed") || !strings.HasPrefix(frame[1], unknown.Target) {
		t.Fatal("The completed download was not folded into the summary.", frame)
	}
}

func TestDownloadTableTerminalBounds(t *testing.T) {
	for _, width := range []int{1, 7, 20, 55, 100} {
		for _, height := range []int{1, 2, 3, 4, 5, 16} {
			live := &liveProgress{width: func() int { return width }, height: func() int { return height }, styles: newTextStyles(io.Discard), mode: "download", started: time.Now()}
			for i := range 10 {
				live.rows = append(live.rows, liveRow{download: &download.Progress{Target: strings.Repeat("包", 50), State: "update", Status: "downloading", Current: int64(i), Total: 10, TotalKnown: i%2 == 0}})
			}
			frame := live.frame()
			if len(frame) > height {
				t.Fatalf("The download table exceeds height %d: %q", height, frame)
			}
			for _, line := range frame {
				if ansi.StringWidth(line) > width {
					t.Fatalf("The download table exceeds width %d: %q", width, line)
				}
			}
		}
	}
}

func TestUnknownDownloadIndicatorAnimation(t *testing.T) {
	live := &liveProgress{styles: newTextStyles(io.Discard), started: time.Now()}
	p := &download.Progress{}
	first := live.downloadIndicator(p, 80)
	live.started = live.started.Add(-pterm.DefaultSpinner.Delay)
	second := live.downloadIndicator(p, 80)
	if first == second || strings.Contains(first+second, "%") {
		t.Fatal("The unknown-length indicator did not animate without a percentage.", first, second)
	}
}

func TestDownloadProgressModesAndCancellation(t *testing.T) {
	for _, mode := range []string{"auto", "plain", "none", "json"} {
		t.Run(mode, func(t *testing.T) {
			var out, errOut bytes.Buffer
			r := &Renderer{Out: &out, Err: &errOut, ProgressMode: mode, JSON: mode == "json"}
			p := download.Progress{TaskID: "voice", Target: "voice=1.0.0.0", State: "update", Status: "downloading", Current: 1 << 20, Total: 4 << 20, TotalKnown: true}
			r.DownloadProgress(p)
			r.Close()
			if strings.Contains(out.String()+errOut.String(), "\x1b") {
				t.Fatal("Terminal controls leaked into redirected output.")
			}
			switch mode {
			case "none":
				if out.Len()+errOut.Len() != 0 {
					t.Fatal("Disabled progress produced output.")
				}
			case "json":
				var event struct {
					Event string
					Data  download.Progress
				}
				if err := json.Unmarshal(out.Bytes(), &event); err != nil || event.Event != "progress" || event.Data.Current != p.Current || event.Data.Total != p.Total || errOut.Len() != 0 {
					t.Fatal("Structured download progress changed.", out.String(), errOut.String(), err)
				}
			default:
				if out.Len() != 0 || !strings.Contains(errOut.String(), "1048576/4194304 bytes") {
					t.Fatal("Redirected download progress lacks byte counts.", out.String(), errOut.String())
				}
			}
		})
	}
	for _, state := range []string{"failed", "cancelled"} {
		var out bytes.Buffer
		live := newLiveProgress(&out, func() int { return 80 }, newTextStyles(&out))
		p := download.Progress{TaskID: "voice", Target: "voice=1.0.0.0", State: "update", Status: "downloading", Current: 1, Total: 4, TotalKnown: true}
		live.download(p)
		p.State = state
		live.download(p)
		_ = live.close()
		if !strings.HasSuffix(out.String(), "\x1b[?25h") || regexp.MustCompile(`100%`).MatchString(out.String()) || !strings.Contains(out.String(), "1 failed") {
			t.Fatal("Download failure lost terminal restoration or reported completion.", out.String())
		}
	}
}

func TestJSONRedactionPreservesInput(t *testing.T) {
	var out bytes.Buffer
	r := Renderer{JSON: true, Out: &out}
	input := map[string]any{"url": "https://user:password@example.com/path?token=secret#fragment", "message": "failed https://example.com/path?key=secret"}
	r.write(input)
	if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "password") {
		t.Fatal(out.String())
	}
	if !strings.Contains(input["url"].(string), "secret") {
		t.Fatal("mutated protocol value")
	}
}
