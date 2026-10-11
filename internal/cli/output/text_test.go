package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"github.com/muesli/termenv"
)

func styledRenderer(out, errOut io.Writer) *Renderer {
	r := &Renderer{Out: out, Err: errOut, Language: "en"}
	r.style, r.errStyle = newTextStyles(out), newTextStyles(errOut)
	r.style.renderer.SetColorProfile(termenv.ANSI)
	r.errStyle.renderer.SetColorProfile(termenv.ANSI)
	return r
}

func TestTextInfoRestoresSectionsAndShowsIndependentLevel(t *testing.T) {
	var out bytes.Buffer
	r := styledRenderer(&out, io.Discard)
	version, _ := packageinfo.ParseVersion("1")
	p := packageinfo.PackageManifest{
		Identity:      packageinfo.Identity{ID: "vendor/voice", Version: version},
		FormatVersion: version, CompatVersion: version, RuntimeLevel: 1,
		Name: packageinfo.Text{"_": "Voice"},
		Contributions: []packageinfo.Contribution{{
			Category: "vendor.custom", ID: "main", Path: "custom.json",
		}, {
			Category: "singer", ID: "voice", Path: "singer.json",
			SingerDeclaration: &packageinfo.SingerDeclaration{Expanded: true, Level: json.Number("2")},
		}},
	}
	if err := r.Query(packagemanager.Info{Package: p, File: "voice.dspk", Hash: strings.Repeat("0", 128)}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Fatal("terminal styling was dropped")
	}
	plain := ansi.Strip(out.String())
	for _, text := range []string{"File\n  Path:", "Package\n  ID: vendor/voice", "Dependencies\n  (none)", "Contributions\n  vendor.custom:", "main -> custom.json", "      Level: 2"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("missing %q in:\n%s", text, plain)
		}
	}
}

func TestTextInfoUsesCompleteTypedMetadata(t *testing.T) {
	for _, url := range []string{"https://example.test/voice", ""} {
		t.Run(url, func(t *testing.T) {
			var out bytes.Buffer
			r := styledRenderer(&out, io.Discard)
			r.Language = "*"
			p := packageinfo.PackageManifest{
				Copyright: packageinfo.Text{"_": "", "en": "Copyright Voice", "zh-CN": "版权"},
				Readme:    packageinfo.Text{"_": "/voice/readme.txt", "zh-CN": "/voice/说明.txt"},
				URL:       &url,
			}
			if err := r.Query(packagemanager.Info{Package: p}, nil); err != nil {
				t.Fatal(err)
			}
			plain := ansi.Strip(out.String())
			for _, text := range []string{"  Copyright[_]: \n", "  Copyright[en]: Copyright Voice\n", "  Copyright[zh-CN]: 版权\n", "  Readme[_]: /voice/readme.txt\n", "  Readme[zh-CN]: /voice/说明.txt\n", "  URL: " + url + "\n"} {
				if !strings.Contains(plain, text) {
					t.Fatalf("typed metadata missing from info: %q in:\n%s", text, plain)
				}
			}
		})
	}
}

func TestTextTableRestoresHeaderAndAllLanguageDisplay(t *testing.T) {
	var out bytes.Buffer
	r := styledRenderer(&out, io.Discard)
	r.Language = "*"
	r.List([]packagemanager.InstalledPackage{{
		Identity: packageinfo.Identity{ID: "voice"},
		Name:     packageinfo.Text{"_": "", "en": "Voice", "zh-CN": "歌手"},
	}}, []string{"id", "version", "name"})
	if err := r.Finish(nil); err != nil {
		t.Fatal(err)
	}
	plain := ansi.Strip(out.String())
	for _, text := range []string{"ID", "Version", "Name", "─", "[_]", "[en] Voice", "[zh-CN] 歌手"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("missing table/language display %q in:\n%s", text, plain)
		}
	}
	if !strings.Contains(out.String(), "\x1b[1m") {
		t.Fatal("table header is not bold")
	}
}

func TestDryRunTextDoesNotReportAppliedResults(t *testing.T) {
	var out bytes.Buffer
	r := styledRenderer(&out, io.Discard)
	r.Command, r.DryRun = "install", true
	action := packagemanager.Action{Package: packageinfo.Identity{ID: "voice"}, Action: "install", File: "voice.dspk"}
	r.Observer(packagemanager.Event{Event: "result", Data: action})
	r.Observer(packagemanager.Event{Event: "summary", Data: packagemanager.MutationResult{Actions: []packagemanager.Action{action}, DryRun: true}})
	plain := ansi.Strip(out.String())
	if !strings.Contains(plain, "[DRY RUN]") || !strings.Contains(plain, "Summary (dry run):") || strings.Contains(plain, "[installed]") {
		t.Fatal("dry-run output claims actual installation:", plain)
	}
}

func TestPackTextDoesNotPrintJSON(t *testing.T) {
	var out bytes.Buffer
	r := styledRenderer(&out, io.Discard)
	r.Command = "pack"
	identity := packageinfo.Identity{ID: "voice"}
	r.Observer(packagemanager.Event{Event: "plan", Data: packagearchive.PackPlan{Files: []string{"desc.json"}, Bytes: 42, Package: packageinfo.PackageManifest{Identity: identity}, Output: "voice.dspk"}})
	r.Observer(packagemanager.Event{Event: "result", Data: map[string]any{"file": "voice.dspk", "hash": strings.Repeat("0", 128), "package": identity}})
	plain := ansi.Strip(out.String())
	for _, text := range []string{"✓ packed voice=0.0.0.0", "Output: voice.dspk", "Files: 1", "Uncompressed size: 42 bytes", "Hash:"} {
		if !strings.Contains(plain, text) {
			t.Fatalf("missing readable pack result %q in:\n%s", text, plain)
		}
	}
	if strings.Contains(plain, `{"`) {
		t.Fatal("text output contains raw event JSON:", plain)
	}
}

func TestStyledJSONHasNoTerminalEscapesOrDiagnosticText(t *testing.T) {
	var out, errOut bytes.Buffer
	r := styledRenderer(&out, &errOut)
	r.Command, r.JSON, r.Mutation = "install", true, true
	r.Observer(packagemanager.Event{Event: "result", Data: packagemanager.Action{Package: packageinfo.Identity{ID: "voice"}, Action: "install", File: "voice.dspk"}})
	r.Diagnostics([]string{"metadata warning"})
	if err := r.Finish(&packagemanager.Error{Code: "IO_ERROR", Message: "operation failed"}); err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), `\u001b`) || errOut.Len() != 0 {
		t.Fatal("styling or diagnostics leaked into JSON/stderr", out.String(), errOut.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("not NDJSON:", line)
		}
	}
}

func TestRedirectedTextHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "")
	var out bytes.Buffer
	r := &Renderer{Out: &out, Err: io.Discard}
	r.List(nil, []string{"id", "version", "name"})
	if err := r.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "Version") {
		t.Fatal("redirected output is not readable plain text", out.String())
	}
}
