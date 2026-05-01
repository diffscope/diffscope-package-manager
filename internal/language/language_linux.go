//go:build linux

package language

import (
	"os"
	"strings"
)

func platformLanguageCodes() []string {
	values := make([]string, 0, 3)
	for _, path := range []string{"/etc/locale.conf", "/etc/default/locale"} {
		values = append(values, readLocaleConfig(path)...)
	}
	return values
}

func readLocaleConfig(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var values []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "LC_ALL" && key != "LC_MESSAGES" && key != "LANGUAGE" && key != "LANG" {
			continue
		}

		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}
