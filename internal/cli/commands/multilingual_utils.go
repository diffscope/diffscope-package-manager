package commands

import (
	"sort"

	internallanguage "diffscope-package-manager/internal/language"
	"diffscope-package-manager/packageinfo"
)

func addMultilingualName(name *packageinfo.MultilingualText, language string, value string) {
	if language == "_" {
		name.Default = value
		return
	}
	name.Texts[language] = value
}

func normalizeMultilingualName(name packageinfo.MultilingualText) *packageinfo.MultilingualText {
	if name.Default == "" && len(name.Texts) == 0 {
		return nil
	}
	if name.Texts == nil {
		name.Texts = make(map[string]string)
	}
	return &name
}

func selectMultilingualText(text *packageinfo.MultilingualText, languageCode string) string {
	values := multilingualTextMap(text)
	if len(values) == 0 {
		return ""
	}

	available := make([]string, 0, len(values))
	for key := range values {
		if key != "_" {
			available = append(available, key)
		}
	}
	if matched, ok := internallanguage.BestMatch(languageCode, available); ok {
		return values[matched]
	}
	return values["_"]
}

func sortedMultilingualKeys(text *packageinfo.MultilingualText) []string {
	values := multilingualTextMap(text)
	keys := make([]string, 0, len(values))
	if _, ok := values["_"]; ok {
		keys = append(keys, "_")
	}
	extraKeys := make([]string, 0, len(values))
	for key := range values {
		if key != "_" {
			extraKeys = append(extraKeys, key)
		}
	}
	sort.Strings(extraKeys)
	keys = append(keys, extraKeys...)
	return keys
}

func multilingualTextMap(text *packageinfo.MultilingualText) map[string]string {
	if text == nil {
		return nil
	}
	values := make(map[string]string, len(text.Texts)+1)
	values["_"] = text.Default
	for key, value := range text.Texts {
		if key == "_" {
			continue
		}
		values[key] = value
	}
	return values
}

func multilingualJSONValue(text *packageinfo.MultilingualText, languageCode string) any {
	if text == nil {
		return nil
	}
	if languageCode == "*" {
		return multilingualTextMap(text)
	}
	return selectMultilingualText(text, languageCode)
}
