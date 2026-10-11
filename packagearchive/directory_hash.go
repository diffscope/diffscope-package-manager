package packagearchive

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

// HashDirectory hashes the staged bytes that will actually be installed.
func HashDirectory(ctx context.Context, root string, limits Limits) (string, error) {
	return HashDirectoryWithProgress(ctx, root, limits, nil)
}

func HashDirectoryWithProgress(ctx context.Context, root string, limits Limits, callback ProgressFunc) (string, error) {
	d, err := OpenDirectory(root)
	if err != nil {
		return "", err
	}
	defer d.Close()
	names := []string{}
	entries := 0
	var size int64
	err = fs.WalkDir(d, ".", func(name string, entry fs.DirEntry, err error) error {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return cancelErr
		}
		if err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		entries++
		if entries > limits.Entries {
			return fmt.Errorf("too many staged entries")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("special staged file")
		}
		if info.Size() > limits.FileBytes || info.Size() > limits.TotalBytes-size {
			return fmt.Errorf("staged size limit exceeded")
		}
		size += info.Size()
		if err := validateName(name, false, limits.PathBytes); err != nil {
			return err
		}
		names = append(names, name)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	progress := newProgress(size, len(names), callback)
	h := sha512.New()
	var total int64
	for _, name := range names {
		progress.file(name)
		f, err := d.Open(name)
		if err != nil {
			return "", err
		}
		fh := sha512.New()
		n, err := copyContextWithProgress(ctx, fh, f, min(limits.FileBytes, limits.TotalBytes-total), progress.add)
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		total += n
		progress.done()
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(fh.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
