package packagearchive

import (
	"archive/zip"
	"bytes"
	"crypto/sha512"
	"fmt"
	"io"
	"sort"
)

// ComputePackageHash computes the deterministic SHA-512 package hash for a zip archive.
//
// Each regular file is hashed with SHA-512 first. The final package hash is SHA-512 over
// entries sorted by their UTF-8 path bytes, where each entry is:
// [path bytes][0x00][64-byte file SHA-512].
func ComputePackageHash(reader ZipReader) ([sha512.Size]byte, error) {
	archive, err := openPackageZip(reader)
	if err != nil {
		return [sha512.Size]byte{}, err
	}

	entries := make([]packageHashEntry, 0, len(archive.File))
	for _, file := range archive.File {
		if err := validateZipFile(file); err != nil {
			return [sha512.Size]byte{}, err
		}
		if file.FileInfo().IsDir() {
			continue
		}

		hash, err := hashZipFile(file)
		if err != nil {
			return [sha512.Size]byte{}, err
		}
		entries = append(entries, packageHashEntry{
			path: []byte(file.Name),
			hash: hash,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].path, entries[j].path) < 0
	})

	packageHasher := sha512.New()
	for _, entry := range entries {
		if _, err := packageHasher.Write(entry.path); err != nil {
			return [sha512.Size]byte{}, fmt.Errorf("hash package path %q: %w", string(entry.path), err)
		}
		if _, err := packageHasher.Write([]byte{0}); err != nil {
			return [sha512.Size]byte{}, fmt.Errorf("hash package separator for %q: %w", string(entry.path), err)
		}
		if _, err := packageHasher.Write(entry.hash[:]); err != nil {
			return [sha512.Size]byte{}, fmt.Errorf("hash package file digest for %q: %w", string(entry.path), err)
		}
	}

	var result [sha512.Size]byte
	copy(result[:], packageHasher.Sum(nil))
	return result, nil
}

type packageHashEntry struct {
	path []byte
	hash [sha512.Size]byte
}

func hashZipFile(file *zip.File) ([sha512.Size]byte, error) {
	stream, err := file.Open()
	if err != nil {
		return [sha512.Size]byte{}, fmt.Errorf("open zip entry %q: %w", file.Name, err)
	}
	defer stream.Close()

	hasher := sha512.New()
	if _, err := io.Copy(hasher, stream); err != nil {
		return [sha512.Size]byte{}, fmt.Errorf("hash zip entry %q: %w", file.Name, err)
	}

	var result [sha512.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result, nil
}
