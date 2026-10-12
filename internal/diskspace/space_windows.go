package diskspace

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

func IsFull(err error) bool {
	return errors.Is(err, syscall.Errno(112)) || errors.Is(err, syscall.Errno(39))
}

func Check(path string, needed int64) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	var available uint64
	call := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	ok, _, err := call.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return err
	}
	if needed < 0 || available < uint64(needed) {
		return fmt.Errorf("DISK_FULL: Available storage is insufficient.")
	}
	return nil
}
