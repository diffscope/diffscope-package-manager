//go:build !windows

package diskspace

import (
	"errors"
	"fmt"
	"syscall"
)

func IsFull(err error) bool { return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) }

func Check(path string, needed int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return err
	}
	if needed < 0 || uint64(st.Bavail)*uint64(st.Bsize) < uint64(needed) {
		return fmt.Errorf("DISK_FULL: Available storage is insufficient.")
	}
	return nil
}
