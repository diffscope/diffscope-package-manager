package cli

import (
	"bytes"
	"encoding/json"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func execute(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	c := NewRootCommand()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	err := ExecuteCommand(c)
	return out.String(), errOut.String(), err
}
func TestLocalSourcesAndJSON(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	filename := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"singer.json": `{"interface":"example.Singer","level":1,"variant":"v","name":{"_":"","zh-CN":"歌手"},"demoAudio":"../../missing"}`})
	out, _, err := execute(t, "--json", "--packages-dir", root, "info", "--file", filename)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(out, err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("info file created local store")
	}
	out, _, err = execute(t, "--json", "--packages-dir", root, "install", "--file", filename)
	if err != nil {
		t.Fatal(out, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("invalid event", line)
		}
	}
	out, _, err = execute(t, "--json", "--language", "zh-CN", "--packages-dir", root, "info", "--installed", "voice=1:singer/main")
	if err != nil || !strings.Contains(out, `"_":""`) || !strings.Contains(out, "歌手") {
		t.Fatal(out, err)
	}
	if err = json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal("query not a single JSON value", out)
	}
	if _, _, err = execute(t, "info", "--file", "--installed", filename); err == nil {
		t.Fatal("source flags not exclusive")
	}
	out, _, err = execute(t, "--json", "--packages-dir", root, "info", "--file", filepath.Join(t.TempDir(), "missing.dspk"))
	if err == nil || !json.Valid([]byte(out)) {
		t.Fatal(out, err)
	}
	for _, command := range []string{"inspect", "search"} {
		out, _, err = execute(t, "--json", command, filename)
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatal("The unsupported command was not rejected.", command, out, err)
		}
	}
	if _, _, err = execute(t, "pack", "--desc-format", "toml", t.TempDir()); err == nil {
		t.Fatal("conversion option retained")
	}
}
func TestRemotePanicHelper(t *testing.T) {
	if os.Getenv("DSPM_REMOTE_HELPER") != "1" {
		return
	}
	args := []string{"--json", os.Getenv("DSPM_REMOTE_COMMAND"), "voice"}
	_, _, _ = execute(t, args...)
}
func TestRemoteBranchesPanic(t *testing.T) {
	for _, command := range []string{"install", "info"} {
		t.Run(command, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestRemotePanicHelper$")
			cmd.Env = append(os.Environ(), "DSPM_REMOTE_HELPER=1", "DSPM_REMOTE_COMMAND="+command)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "panic: not implemented") {
				t.Fatal(string(output), err)
			}
			if strings.Contains(string(output), "FEATURE_NOT_IMPLEMENTED") || strings.Contains(string(output), `"error":`) {
				t.Fatal("panic translated into a CLI result", string(output))
			}
		})
	}
}

func TestListAliasesAndColumnNormalization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "packages")
	for _, args := range [][]string{{"ls", "--column", " ID, Version, installed_at "}, {"list", "--columns", "InstalledAt, compatVersion"}, {"ls", "--column", ""}} {
		out, _, err := execute(t, append([]string{"--packages-dir", root}, args...)...)
		if err != nil {
			t.Fatal(args, out, err)
		}
	}
	out, _, err := execute(t, "--json", "--packages-dir", root, "ls")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, `"command":"list"`) {
		t.Fatal(out, err)
	}
	out, _, err = execute(t, "--packages-dir", root, "remove", "missing", "--ignore-non-existent")
	if err != nil || !strings.Contains(out, "Ignored missing packages") || !strings.Contains(out, "1 ignored") {
		t.Fatal(out, err)
	}
}
