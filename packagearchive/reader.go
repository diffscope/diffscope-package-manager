package packagearchive

import (
	"archive/zip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
)

// Returns an archive over the borrowed ReaderAt or an error for invalid archive data.
// The caller retains ownership of r and must keep its bytes stable until archive access ends.
// Closing the archive does not close r.
func NewReaderAt(r io.ReaderAt, size int64, limits Limits) (*Archive, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if size < 0 {
		return nil, fmt.Errorf("The archive size must not be negative.")
	}
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, err
	}
	a := &Archive{files: map[string]*zip.File{}, reader: z, limits: limits}
	if err = a.preflight(); err != nil {
		return nil, err
	}
	return a, nil
}

// Returns the SHA-512 digest of exactly size bytes or an error for invalid size or failed reads.
// The caller must keep the supplied bytes stable during hashing.
func HashFileBytes(ctx context.Context, r io.ReaderAt, size int64, progress ProgressFunc) (string, error) {
	if size < 0 {
		return "", fmt.Errorf("The byte count must not be negative.")
	}
	h := sha512.New()
	b := make([]byte, 128<<10)
	var offset int64
	for offset < size {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := int64(len(b))
		if n > size-offset {
			n = size - offset
		}
		got, err := r.ReadAt(b[:n], offset)
		if int64(got) != n {
			return "", io.ErrUnexpectedEOF
		}
		if err != nil && err != io.EOF {
			return "", err
		}
		h.Write(b[:got])
		offset += int64(got)
		if progress != nil {
			progress(Progress{Current: offset, Total: size})
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
