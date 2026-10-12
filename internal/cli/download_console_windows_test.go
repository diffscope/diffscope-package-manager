//go:build windows

package cli

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestWindowsConsoleRemoteDownloads(t *testing.T) {
	t.Setenv("DSPM_CONSOLE_HELPER", "1")
	t.Setenv("DSPM_JSON", "false")
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	known := testutil.Package(t, "console-known", "1", "1", nil, map[string]string{"resource.bin": strings.Repeat("k", 2<<20)})
	unknown := testutil.Package(t, "console-unknown", "1", "1", []map[string]string{{"id": "console-known", "version": "1"}}, map[string]string{"resource.bin": strings.Repeat("u", 2<<20)})
	server := testutil.ServeRepository(t, known, unknown)
	server.ArchiveHandler = func(w http.ResponseWriter, r *http.Request, data []byte) {
		if strings.Contains(r.URL.Path, "/console-known/") {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		}
		for offset := 0; offset < len(data); offset += 64 << 10 {
			if _, err := w.Write(data[offset:min(len(data), offset+(64<<10))]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(40 * time.Millisecond):
			}
		}
	}
	root := t.TempDir()
	args := []string{"--packages-dir", filepath.Join(root, "packages"), "--cache-dir", filepath.Join(root, "cache"), "--config", filepath.Join(root, "absent-config.json"), "--registry", server.Server.URL + "/packages/", "--allow-unrestricted-downloads", "install", "console-unknown"}
	save := func(name, output string) {
		if directory := os.Getenv("DSPM_TEST_CONSOLE_CAPTURE_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, name+".txt"), []byte(output), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	output, exit := consoleRun(t, "", args...)
	save("remote-downloads", output)
	installAt := strings.Index(output, ": extracting")
	if exit != 0 || installAt < 0 || !strings.Contains(output, "console-known=1.0.0.0 [installed]") || !strings.Contains(output, "console-unknown=1.0.0.0 [installed]") {
		t.Fatal("The remote console installation did not complete.", exit, output)
	}
	downloadOutput := output[:installAt]
	partial := regexp.MustCompile(`\b[1-9][0-9]?%`)
	percentages := map[string]bool{}
	for _, percentage := range partial.FindAllString(downloadOutput, -1) {
		percentages[percentage] = true
	}
	if len(percentages) < 3 || !strings.Contains(downloadOutput, "█") || !strings.Contains(downloadOutput, "Downloads:") || !strings.Contains(downloadOutput, "console-known=1.0.0.0") || !strings.Contains(downloadOutput, "console-unknown=1.0.0.0") || !strings.Contains(downloadOutput, "Total size unknown.") {
		t.Fatal("The download stage lacks determinate or indeterminate progress.", downloadOutput)
	}
	if !strings.Contains(output[installAt:], "100%") || !strings.Contains(output, "\x1b[?25h") {
		t.Fatal("The installation progress or cursor restoration is absent.", output)
	}
	knownPath, unknownPath := "/archives/console-known/1.0.0.0", "/archives/console-unknown/1.0.0.0"
	if server.Count(knownPath) != 1 || server.Count(unknownPath) != 1 {
		t.Fatal("The test did not perform both archive downloads.")
	}
	args[1] = filepath.Join(root, "cached-packages")
	output, exit = consoleRun(t, "", args...)
	save("remote-cache-hit", output)
	if exit != 0 || !strings.Contains(output, "console-known=1.0.0.0 [installed]") || !strings.Contains(output, "console-unknown=1.0.0.0 [installed]") || server.Count(knownPath) != 1 || server.Count(unknownPath) != 1 {
		t.Fatal("Cache reuse failed or requested an archive again.", exit, output)
	}
	args[1], args[3] = filepath.Join(root, "cancelled-packages"), filepath.Join(root, "cancelled-cache")
	args[len(args)-1] = "console-known"
	output, exit = consoleRun(t, " KiB/s ETA ", args...)
	save("remote-download-cancelled", output)
	if exit != 1 || !strings.Contains(output, "canceled") || !strings.Contains(output, "\x1b[?25h") || strings.Contains(output, ": extracting") || strings.Contains(output, "[installed]") || strings.Contains(output, "100%") {
		t.Fatal("Download cancellation lost terminal restoration or entered installation.", exit, output)
	}
	if entries, err := os.ReadDir(args[1]); err == nil {
		for _, entry := range entries {
			if entry.Name() != ".dspm" {
				t.Fatal("An installed package survived download cancellation.", entry.Name())
			}
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
