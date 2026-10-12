package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteConfigurationPrecedenceAndSourceReplacement(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	raw := []byte(`{"configurationVersion":1,"registries":[{"name":"stored","url":"https://stored.example/packages/"}],"downloadConcurrency":2}`)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSPM_REGISTRIES", `["https://environment.example/packages/"]`)
	t.Setenv("DSPM_REGISTRY", "https://conflicting.example/")
	cache := filepath.Join(t.TempDir(), "cache")
	r, err := LoadRemote(file, []string{"https://flag.example/a/../packages"}, true, cache, true, 3, true)
	if err != nil || len(r.Registries) != 1 || r.Registries[0].URL != "https://flag.example/packages/" || r.DownloadConcurrency != 3 || r.CacheDir != cache {
		t.Fatal(r, err)
	}
	if _, err = LoadRemote(file, nil, false, cache, true, 0, false); err == nil {
		t.Fatal("conflicting environment accepted")
	}
	if _, err = LoadRemote(file, []string{"https://flag.example/a/../packages/", "https://flag.example/packages/"}, true, cache, true, 0, false); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(file)
	if string(after) != string(raw) {
		t.Fatal("configuration rewritten")
	}
	if _, err = os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("configuration loading created cache")
	}
}
