package packagearchive

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (a *Archive) Extract(ctx context.Context, dest string) error {
	return a.ExtractWithProgress(ctx, dest, nil)
}

func (a *Archive) ExtractWithProgress(ctx context.Context, dest string, callback ProgressFunc) error {
	_, err := a.ExtractAndHash(ctx, dest, callback)
	return err
}

// Extracts files and returns the content-tree digest of the successfully written streams.
// Returns an error for invalid streams, failed writes, synchronization errors, or cancellation.
func (a *Archive) ExtractAndHash(ctx context.Context, dest string, callback ProgressFunc) (string, error) {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return "", err
	}
	defer root.Close()
	for _, entry := range a.reader.File {
		if entry.Mode().IsDir() {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if err := root.MkdirAll(filepath.FromSlash(strings.TrimSuffix(entry.Name, "/")), 0o755); err != nil {
				return "", err
			}
		}
	}
	tree := sha512.New()
	var total int64
	names := make([]string, 0, len(a.files))
	for n := range a.files {
		names = append(names, n)
	}
	sort.Strings(names)
	progress := newProgress(a.byteSize(), len(names), callback)
	for _, name := range names {
		progress.file(name)
		target := filepath.FromSlash(name)
		if err := root.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		f, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return "", err
		}
		r, err := a.Files().Open(name)
		if err != nil {
			f.Close()
			return "", err
		}
		fileHash := sha512.New()
		n, copyErr := copyContextWithProgress(ctx, io.MultiWriter(f, fileHash), r, a.limits.TotalBytes-total, progress.add)
		total += n
		readErr := r.Close()
		syncErr := f.Sync()
		closeErr := f.Close()
		for _, e := range []error{copyErr, readErr, syncErr, closeErr} {
			if e != nil {
				return "", e
			}
		}
		tree.Write([]byte(name))
		tree.Write([]byte{0})
		tree.Write(fileHash.Sum(nil))
		progress.done()
	}
	return hex.EncodeToString(tree.Sum(nil)), nil
}
