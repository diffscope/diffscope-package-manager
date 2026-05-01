//go:build windows

package language

import (
	"syscall"
	"unsafe"
)

const localeNameMaxLength = 85

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
)

func platformLanguageCodes() []string {
	name, ok := userDefaultLocaleName()
	if !ok {
		return nil
	}
	return []string{name}
}

func userDefaultLocaleName() (string, bool) {
	buffer := make([]uint16, localeNameMaxLength)
	result, _, _ := procGetUserDefaultLocaleName.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	if result == 0 {
		return "", false
	}
	return syscall.UTF16ToString(buffer), true
}
