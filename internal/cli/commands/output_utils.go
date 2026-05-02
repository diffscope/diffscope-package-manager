package commands

import (
	"fmt"
	"io"
	"strings"

	"diffscope-package-manager/packageinfo"

	"github.com/charmbracelet/lipgloss"
)

var (
	inspectSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("6"))
	inspectKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))
	inspectLanguageTagStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("5"))
	inspectEmptyStyle = lipgloss.NewStyle().
				Faint(true)
	inspectOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2"))
	inspectWarningStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("3"))
	inspectErrorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("1"))
)

func printSectionTitle(out io.Writer, title string) {
	fmt.Fprintln(out, inspectSectionStyle.Render(title))
}

func printSubsectionLabel(out io.Writer, indent string, label string) {
	fmt.Fprintf(out, "%s%s\n", indent, inspectKeyStyle.Render(label+":"))
}

func printField(out io.Writer, indent string, key string, value string) {
	fmt.Fprintf(out, "%s%s %s\n", indent, inspectKeyStyle.Render(key+":"), value)
}

func printMultilingualField(out io.Writer, indent string, key string, languageKey string, value string) {
	renderedKey := inspectKeyStyle.Render(key) +
		inspectLanguageTagStyle.Render(fmt.Sprintf("[%s]", languageKey)) +
		inspectKeyStyle.Render(":")
	fmt.Fprintf(out, "%s%s %s\n", indent, renderedKey, value)
}

func printEmpty(out io.Writer, indent string) {
	fmt.Fprintf(out, "%s%s\n", indent, inspectEmptyStyle.Render("(none)"))
}

func printOptionalText(out io.Writer, label string, text *packageinfo.MultilingualText, languageCode string) {
	if text == nil {
		return
	}
	printRequiredText(out, label, text, languageCode)
}

func printRequiredText(out io.Writer, label string, text *packageinfo.MultilingualText, languageCode string) {
	if languageCode == "*" {
		values := multilingualTextMap(text)
		indent, labelKey := splitTextLabel(label)
		for _, languageKey := range sortedMultilingualKeys(text) {
			printMultilingualField(out, indent, labelKey, languageKey, values[languageKey])
		}
		return
	}
	indent, key := splitTextLabel(label)
	printField(out, indent, key, selectMultilingualText(text, languageCode))
}

func referenceDisplay(name *packageinfo.MultilingualText, reference packageinfo.PackageReference, languageCode string) string {
	if name == nil {
		return reference.String()
	}
	if languageCode == "*" {
		return fmt.Sprintf("%s (%s)", inlineMultilingualText(name), reference.String())
	}
	return fmt.Sprintf("%s (%s)", selectMultilingualText(name, languageCode), reference.String())
}

func inlineMultilingualText(text *packageinfo.MultilingualText) string {
	values := multilingualTextMap(text)
	parts := make([]string, 0, len(values))
	for _, languageKey := range sortedMultilingualKeys(text) {
		tag := inspectLanguageTagStyle.Render(fmt.Sprintf("[%s]", languageKey))
		parts = append(parts, fmt.Sprintf("%s %s", tag, values[languageKey]))
	}
	return strings.Join(parts, " ")
}

func splitTextLabel(label string) (string, string) {
	trimmed := strings.TrimLeft(label, " ")
	return label[:len(label)-len(trimmed)], trimmed
}
