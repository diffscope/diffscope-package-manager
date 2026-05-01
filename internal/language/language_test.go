package language

import "testing"

func TestNormalizeCodeReturnsCanonicalBCP47(t *testing.T) {
	tests := map[string]string{
		"en_US.UTF-8": "en-US",
		"zh_CN":       "zh-CN",
		"zh-Hans-CN":  "zh-Hans-CN",
		"fr_FR@euro":  "fr-FR",
		"pt_BR":       "pt-BR",
	}

	for input, want := range tests {
		got, ok := normalizeCode(input)
		if !ok {
			t.Fatalf("normalizeCode(%q) ok = false", input)
		}
		if got != want {
			t.Fatalf("normalizeCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeCodeRejectsPOSIXLocales(t *testing.T) {
	for _, input := range []string{"", "C", "POSIX", "C.UTF-8"} {
		if got, ok := normalizeCode(input); ok {
			t.Fatalf("normalizeCode(%q) = %q, true; want false", input, got)
		}
	}
}

func TestCurrentCodeReturnsBCP47(t *testing.T) {
	got := CurrentCode()
	if _, ok := normalizeCode(got); !ok {
		t.Fatalf("CurrentCode() = %q, not BCP 47", got)
	}
}
