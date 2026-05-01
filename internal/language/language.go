package language

import (
	"os"
	"strings"

	xlanguage "golang.org/x/text/language"
)

const defaultLanguage = "und"

// CurrentCode returns the best available BCP 47 language code for the current process.
func CurrentCode() string {
	for _, candidate := range append(platformLanguageCodes(), environmentLanguageCodes()...) {
		for _, value := range splitLanguageValue(candidate) {
			if tag, ok := normalizeCode(value); ok {
				return tag
			}
		}
	}
	return defaultLanguage
}

// BestMatch returns the available BCP 47 code that best matches requested.
func BestMatch(requested string, available []string) (string, bool) {
	if len(available) == 0 {
		return "", false
	}
	if strings.TrimSpace(requested) == "" || normalizeBCP47(requested) == defaultLanguage {
		return "", false
	}

	tags := make([]xlanguage.Tag, 0, len(available))
	indexes := make([]int, 0, len(available))
	for index, code := range available {
		tag, err := xlanguage.Parse(normalizeBCP47(code))
		if err != nil {
			continue
		}
		tags = append(tags, tag)
		indexes = append(indexes, index)
	}
	if len(tags) == 0 {
		return "", false
	}

	requestedTag, err := xlanguage.Parse(normalizeBCP47(requested))
	if err != nil {
		requestedTag = xlanguage.Und
	}

	_, matchedIndex, confidence := xlanguage.NewMatcher(tags).Match(requestedTag)
	if confidence == xlanguage.No {
		return "", false
	}
	return available[indexes[matchedIndex]], true
}

func environmentLanguageCodes() []string {
	values := make([]string, 0, 4)
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		if value := os.Getenv(key); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func splitLanguageValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ':' || r == ';' || r == ','
	})
}

func normalizeCode(value string) (string, bool) {
	value = normalizeBCP47(value)
	if value == "" || value == "C" || value == "POSIX" {
		return "", false
	}
	tag, err := xlanguage.Parse(value)
	if err != nil {
		return "", false
	}
	return tag.String(), true
}

func normalizeBCP47(value string) string {
	value = strings.TrimSpace(value)
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		value = value[:dot]
	}
	if at := strings.IndexByte(value, '@'); at >= 0 {
		value = value[:at]
	}
	return strings.ReplaceAll(value, "_", "-")
}
