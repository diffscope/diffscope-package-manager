package output

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"github.com/gookit/color"
	"github.com/pterm/pterm"
)

func TestLegacyProgressBarGeometry(t *testing.T) {
	// Captured from the old PTerm configuration: width 60, total 1000,
	// no count/title/timer, full block glyphs, percentage on the right.
	for _, sample := range []struct {
		current, blocks int
		suffix          string
	}{{0, 53, "   0% "}, {250, 54, "  25% "}, {1000, 54, " 100% "}} {
		got := ansi.Strip(originalProgressBar(60, sample.current))
		want := strings.Repeat("█", sample.blocks) + sample.suffix
		if got != want {
			t.Fatalf("legacy progress frame changed:\ngot  %q\nwant %q", got, want)
		}
	}
}

func TestLegacyBatchLayout(t *testing.T) {
	fixture, err := os.ReadFile("testdata/legacy-install-frame.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(fixture), "\r\n", "\n")
	for _, format := range []struct {
		name, newline string
	}{{"LF", "\n"}, {"CRLF", "\r\n"}} {
		t.Run(format.name, func(t *testing.T) {
			testLegacyBatchLayout(t, strings.ReplaceAll(text, "\n", format.newline))
		})
	}
}

func testLegacyBatchLayout(t *testing.T, fixture string) {
	t.Helper()
	styles := newTextStyles(io.Discard)
	l := &liveProgress{width: func() int { return 60 }, styles: styles, mode: "extract", rows: []liveRow{
		{target: "vendor/voice=1.0.0.0", file: "音源/resource.bin", current: 250},
		{target: "vendor/base=2.0.0.0"},
	}}
	// Fixture line endings may be LF or CRLF. Trailing spaces are excluded
	// because TestLegacyProgressBarGeometry checks the exact bar suffix.
	actual := l.frame()
	fixture = strings.ReplaceAll(fixture, "\r\n", "\n")
	expected := strings.Split(strings.TrimSuffix(fixture, "\n"), "\n")
	if len(actual) != len(expected) {
		t.Fatal("batch no longer has a title and bar per package", actual)
	}
	for i := range actual {
		if strings.TrimRight(ansi.Strip(actual[i]), " ") != strings.TrimRight(expected[i], " ") {
			t.Fatalf("legacy row %d changed: %q", i, actual[i])
		}
	}
	l.mode = "pack"
	l.rows = l.rows[:1]
	if got := ansi.Strip(l.frame()[0]); got != "vendor/voice=1.0.0.0: packing 音源/resource.bin" {
		t.Fatal(got)
	}
	l.mode = "inspect"
	l.rows = []liveRow{{target: "first.dspk"}, {target: "second.dspk"}}
	l.started = time.Now()
	for i, got := range l.frame() {
		if ansi.Strip(got) != "▀  Inspecting "+l.rows[i].target {
			t.Fatal("legacy spinner changed", got)
		}
	}
}

func TestLegacyColorHelper(t *testing.T) {
	if os.Getenv("DSPM_LEGACY_COLOR_HELPER") != "1" {
		return
	}
	color.ForceColor()
	color.Enable = true
	// Recreate the old default, initialized while color is available.
	pterm.DefaultProgressbar.BarFiller = pterm.Gray("█")
	want := "\x1b[36m\x1b[36m" + strings.Repeat("█", 14) + "\x1b[0m\x1b[0m" + strings.Repeat("\x1b[90m█\x1b[0m", 40) + " \x1b[38;2;191;63;0m 25%\x1b[0m "
	if got := originalProgressBar(60, 250); got != want {
		t.Fatalf("legacy progress colors changed:\ngot  %q\nwant %q", got, want)
	}
}
func TestLegacyProgressColors(t *testing.T) {
	child := exec.Command(os.Args[0], "-test.run=^TestLegacyColorHelper$")
	child.Env = append(os.Environ(), "DSPM_LEGACY_COLOR_HELPER=1")
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatal(string(output), err)
	}
}

func TestLegacyBatchSurvivesVerificationUntilResults(t *testing.T) {
	var out bytes.Buffer
	r := &Renderer{Command: "install", Out: &out, Err: io.Discard, progressInitialized: true}
	r.live = newLiveProgress(&out, func() int { return 60 }, newTextStyles(&out))
	defer r.Close()
	v, _ := packageinfo.ParseVersion("1")
	identity := packageinfo.Identity{ID: "voice", Version: v}
	r.Observer(packagemanager.Event{Event: "plan", Data: []packagemanager.Action{{Package: identity, Action: "install"}}})
	p := progressSample("done", 1000)
	p.Target = identity.String()
	r.Progress(p)
	p.Phase = "verify"
	p.State = "start"
	r.Progress(p)
	r.Observer(packagemanager.Event{Event: "staged", Data: identity})
	r.live.mu.Lock()
	active := r.live.active
	r.live.mu.Unlock()
	if !active {
		t.Fatal("batch bars disappeared before commit/results")
	}
	r.Observer(packagemanager.Event{Event: "result", Data: packagemanager.Action{Package: identity, Action: "install"}})
	r.Close()
	s := ansi.Strip(out.String())
	if strings.Contains(s, "Verifying") || strings.Contains(s, "Staged") || strings.Contains(s, "MiB/") {
		t.Fatal("added UI replaced the legacy presentation", s)
	}
}
