//go:build darwin

package language

import (
	"os/exec"
	"strings"
)

func platformLanguageCodes() []string {
	var values []string

	if output, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output(); err == nil {
		if value := strings.TrimSpace(string(output)); value != "" {
			values = append(values, value)
		}
	}

	if output, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output(); err == nil {
		values = append(values, parseAppleLanguages(string(output))...)
	}

	return values
}

func parseAppleLanguages(value string) []string {
	lines := strings.Split(value, "\n")
	languages := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, `",`)
		if line == "" || line == "(" || line == ")" {
			continue
		}
		languages = append(languages, line)
	}
	return languages
}
