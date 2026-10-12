package packagearchive

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
)

// Returns the content-tree digest after validating all file streams and CRCs.
// Returns an error for failed reads, invalid lengths, CRC failures, or cancellation.
func (a *Archive) Hash(ctx context.Context) (string, error) {
	return a.HashWithProgress(ctx, nil)
}

func (a *Archive) HashWithProgress(ctx context.Context, callback ProgressFunc) (string, error) {
	names := make([]string, 0, len(a.files))
	for name := range a.files {
		names = append(names, name)
	}
	sort.Strings(names)
	progress := newProgress(a.byteSize(), len(names), callback)
	h := sha512.New()
	var total int64
	for _, name := range names {
		progress.file(name)
		r, err := a.Files().Open(name)
		if err != nil {
			return "", err
		}
		fileHash := sha512.New()
		n, err := copyContextWithProgress(ctx, fileHash, r, a.limits.TotalBytes-total, progress.add)
		closeErr := r.Close()
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		if closeErr != nil {
			return "", closeErr
		}
		total += n
		progress.done()
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(fileHash.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func copyContext(ctx context.Context, w io.Writer, r io.Reader, limit int64) (int64, error) {
	return copyContextWithProgress(ctx, w, r, limit, nil)
}

func copyContextWithProgress(ctx context.Context, w io.Writer, r io.Reader, limit int64, progress func(int64)) (int64, error) {
	var total int64
	b := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := r.Read(b)
		if int64(n) > limit-total {
			return total, fmt.Errorf("The stream exceeds its size limit.")
		}
		if n > 0 {
			m, e := w.Write(b[:n])
			total += int64(m)
			if progress != nil && m > 0 {
				progress(int64(m))
			}
			if e != nil {
				return total, e
			}
			if m != n {
				return total, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
