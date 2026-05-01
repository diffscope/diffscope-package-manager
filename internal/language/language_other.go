//go:build !darwin && !linux && !windows

package language

func platformLanguageCodes() []string {
	return nil
}
