package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/registry"
	"github.com/diffscope/diffscope-package-manager/packageinfo"
	"os"
	"path/filepath"
	"runtime"
)

type Registry struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url"`
}
type Remote struct {
	ConfigurationVersion int             `json:"configurationVersion"`
	Registries           []Registry      `json:"registries"`
	CacheDir             string          `json:"cacheDir,omitempty"`
	DownloadConcurrency  int             `json:"downloadConcurrency,omitempty"`
	MaxDownloadBytes     int64           `json:"maxDownloadBytes,omitempty"`
	AllowedDownloadHosts []string        `json:"allowedDownloadHosts,omitempty"`
	SourceRules          []registry.Rule `json:"sourceRules,omitempty"`
}

func DefaultCacheDir() string {
	if runtime.GOOS == "windows" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base, _ = os.UserCacheDir()
		}
		return filepath.Join(base, "OpenVPI", "dspm", "Cache")
	}
	base, _ := os.UserCacheDir()
	return filepath.Join(base, "OpenVPI", "dspm")
}
func ConfigFile() string {
	base, _ := os.UserConfigDir()
	return filepath.Join(base, "OpenVPI", "dspm", "config.json")
}

// Returns the effective remote configuration or an error for invalid settings.
// Loading does not write configuration. Flag overrides precede environment
// conflict checks. Source lists replace the complete configured list.
func LoadRemote(configFile string, registries []string, registryFlag bool, cache string, cacheFlag bool, concurrency int, concurrencyFlag bool) (Remote, error) {
	r := Remote{ConfigurationVersion: 1, Registries: []Registry{{Name: "ovcpr", URL: "https://ovcpr.diffscope.org/packages/"}}, CacheDir: DefaultCacheDir(), DownloadConcurrency: 4, MaxDownloadBytes: 64 << 30}
	if configFile == "" {
		configFile = ConfigFile()
	}
	b, err := os.ReadFile(configFile)
	if err == nil {
		if _, err = packageinfo.DecodeJSON(b); err != nil {
			return r, err
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if err = d.Decode(&r); err != nil {
			return r, err
		}
		if r.ConfigurationVersion != 1 {
			return r, fmt.Errorf("The configurationVersion value is unsupported.")
		}
	} else if !os.IsNotExist(err) {
		return r, err
	}
	if registryFlag {
		r.Registries = []Registry{}
		for _, u := range registries {
			r.Registries = append(r.Registries, Registry{URL: u})
		}
	} else {
		multiple, multiSet := os.LookupEnv("DSPM_REGISTRIES")
		single, singleSet := os.LookupEnv("DSPM_REGISTRY")
		if multiSet && singleSet {
			return r, fmt.Errorf("DSPM_REGISTRIES and DSPM_REGISTRY cannot coexist.")
		}
		if multiSet {
			var urls []string
			if err = json.Unmarshal([]byte(multiple), &urls); err != nil || urls == nil {
				return r, fmt.Errorf("DSPM_REGISTRIES must be a JSON array of URLs.")
			}
			r.Registries = []Registry{}
			for _, u := range urls {
				r.Registries = append(r.Registries, Registry{URL: u})
			}
		} else if singleSet {
			r.Registries = []Registry{{URL: single}}
		}
	}
	if cacheFlag {
		r.CacheDir = cache
	} else if v := os.Getenv("DSPM_CACHE_DIR"); v != "" {
		r.CacheDir = v
	}
	if concurrencyFlag {
		r.DownloadConcurrency = concurrency
	}
	if r.DownloadConcurrency < 1 || r.DownloadConcurrency > 16 {
		return r, fmt.Errorf("Download concurrency must be between 1 and 16.")
	}
	if r.MaxDownloadBytes <= 0 || r.MaxDownloadBytes > 1<<53-1 {
		return r, fmt.Errorf("The download budget is invalid.")
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	for i, s := range r.Registries {
		u, e := registry.Normalize(s.URL)
		if e != nil {
			return r, e
		}
		if seen[u] {
			return r, fmt.Errorf("The source list contains a duplicate registry URL.")
		}
		seen[u] = true
		r.Registries[i].URL = u
		if s.Name != "" {
			if names[s.Name] {
				return r, fmt.Errorf("The source list contains a duplicate source name.")
			}
			names[s.Name] = true
		}
	}
	for _, rule := range r.SourceRules {
		prefix := rule.Prefix
		if len(prefix) > 0 && prefix[len(prefix)-1] == '/' {
			prefix = prefix[:len(prefix)-1]
		}
		if !packageinfo.IsValidPackageIdentifier(prefix) || len(rule.Sources) == 0 {
			return r, fmt.Errorf("The sourceRules value is invalid.")
		}
	}
	r.CacheDir, err = filepath.Abs(r.CacheDir)
	if err != nil {
		return r, err
	}
	return r, nil
}
