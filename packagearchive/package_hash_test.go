package packagearchive

import (
	"bytes"
	"crypto/sha512"
	"os"
	"sort"
	"testing"
)

func TestComputePackageHash(t *testing.T) {
	archive := makeZipArchive(t, []zipTestFile{
		{name: "b.txt", body: "second"},
		{name: "dir/", mode: os.ModeDir | 0755},
		{name: "a.txt", body: "first"},
		{name: "中.txt", body: "utf8"},
	})

	got, err := ComputePackageHash(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("ComputePackageHash() error = %v", err)
	}

	want := expectedPackageHash(map[string]string{
		"a.txt": "first",
		"b.txt": "second",
		"中.txt": "utf8",
	})
	if got != want {
		t.Fatalf("ComputePackageHash() = %x, want %x", got, want)
	}
}

func expectedPackageHash(files map[string]string) [sha512.Size]byte {
	type entry struct {
		path string
		hash [sha512.Size]byte
	}

	entries := make([]entry, 0, len(files))
	for path, body := range files {
		entries = append(entries, entry{
			path: path,
			hash: sha512.Sum512([]byte(body)),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare([]byte(entries[i].path), []byte(entries[j].path)) < 0
	})

	hasher := sha512.New()
	for _, entry := range entries {
		hasher.Write([]byte(entry.path))
		hasher.Write([]byte{0})
		hasher.Write(entry.hash[:])
	}

	var result [sha512.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}
