package packagearchive

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const desc = `{"$version":"1","runtimeLevel":1,"id":"vendor/voice","version":"1","contributions":{"singer":[{"id":"main","path":"singer.json"}]}}`
const singer = `{"name":"Voice","avatar":"../../missing.png","imports":null}`

type zipEntry struct {
	name, data string
	mode       os.FileMode
}

func zipFile(t *testing.T, entries ...zipEntry) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "test.dspk")
	f, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Store}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		s, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		s.Write([]byte(e.data))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return filename
}
func TestRejectArchivePathsAndCollisions(t *testing.T) {
	cases := [][]zipEntry{{{"../x", "", 0}}, {{"/x", "", 0}}, {{"a\\b", "", 0}}, {{"C:x", "", 0}}, {{"a//b", "", 0}}, {{"a/./b", "", 0}}, {{"a/../b", "", 0}}, {{"a\x00b", "", 0}}, {{"NUL.txt", "", 0}}, {{"a.", "", 0}}, {{"x", "", 0}, {"x", "", 0}}, {{"x", "", 0}, {"x/a", "", 0}}, {{"Foo/a", "", 0}, {"foo/b", "", 0}}, {{"desc.json/", "", os.ModeDir}}, {{"link", "target", os.ModeSymlink | 0o777}}, {{"DESC.JSON", desc, 0}}, {{string([]byte{0xff}), "", 0}}}
	for _, entries := range cases {
		filename := zipFile(t, append([]zipEntry{{"desc.json", desc, 0}}, entries...)...)
		if a, err := Open(filename, DefaultLimits()); err == nil {
			a.Close()
			t.Fatal("accepted", entries)
		}
	}
}
func TestLimitsCRCAndCancellation(t *testing.T) {
	filename := zipFile(t, zipEntry{"desc.json", desc, 0}, zipEntry{"singer.json", singer, 0}, zipEntry{"resource.bin", "PAYLOAD", 0})
	limits := DefaultLimits()
	limits.Entries = 2
	if a, err := Open(filename, limits); err == nil {
		a.Close()
		t.Fatal("entry limit")
	}
	limits = DefaultLimits()
	limits.FileBytes = 4
	if a, err := Open(filename, limits); err == nil {
		a.Close()
		t.Fatal("file limit")
	}
	a, err := Open(filename, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Hash(ctx); err == nil {
		t.Fatal("ignored cancellation")
	}
	a.Close()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(data), "PAYLOAD")
	if i < 0 {
		t.Fatal("fixture")
	}
	data[i] = 'X'
	os.WriteFile(filename, data, 0o644)
	a, err = Open(filename, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Hash(context.Background()); err == nil {
		t.Fatal("CRC corruption accepted")
	}
}

func TestImplicitDirectoriesCountTowardEntryLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		entries []zipEntry
		count   int
	}{
		{"implicit", []zipEntry{{"a/b/resource", "data", 0}}, 4},
		{"shared parents", []zipEntry{{"a/b/first", "1", 0}, {"a/b/second", "2", 0}}, 5},
		{"explicit parents", []zipEntry{{"a/", "", os.ModeDir}, {"a/b/", "", os.ModeDir}, {"a/b/resource", "data", 0}}, 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			filename := zipFile(t, append([]zipEntry{{"desc.json", `{}`, 0}}, test.entries...)...)
			limits := DefaultLimits()
			limits.Entries = test.count - 1
			if a, err := Open(filename, limits); err == nil {
				a.Close()
				t.Fatal("preflight omitted implicit directories from the installed entry count")
			}
			limits.Entries = test.count
			a, err := Open(filename, limits)
			if err != nil {
				t.Fatal("shared or explicit parents were counted more than once", err)
			}
			defer a.Close()
			dest := filepath.Join(t.TempDir(), "stage")
			if err := a.Extract(context.Background(), dest); err != nil {
				t.Fatal(err)
			}
			archiveHash, err := a.Hash(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			stagedHash, err := HashDirectory(context.Background(), dest, limits)
			if err != nil || stagedHash != archiveHash {
				t.Fatal("preflight and staged verification disagree about the entry budget", err)
			}
		})
	}
}

func TestActualSizeMismatch(t *testing.T) {
	filename := zipFile(t, zipEntry{"desc.json", desc, 0}, zipEntry{"singer.json", singer, 0})
	data, _ := os.ReadFile(filename)
	// Corrupt the central directory's uncompressed size without changing its stream.
	for i := 0; i+46 < len(data); i++ {
		if string(data[i:i+4]) == "PK\x01\x02" {
			binary.LittleEndian.PutUint32(data[i+24:i+28], 1)
			break
		}
	}
	os.WriteFile(filename, data, 0o644)
	a, err := Open(filename, DefaultLimits())
	if err != nil {
		return
	}
	defer a.Close()
	if _, err = a.Hash(context.Background()); err == nil {
		t.Fatal("size mismatch accepted")
	}
}
func TestPackExtractHashAndNoClobber(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"desc.json": desc, "singer.json": singer, "resource.bin": "bytes"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(source, "result.dspk")
	os.WriteFile(output, []byte("old output"), 0o644)
	plan, err := Plan(context.Background(), source, output, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 3 {
		t.Fatal("output included", plan.Files)
	}
	if _, err := Pack(context.Background(), plan, false, DefaultLimits()); err == nil {
		t.Fatal("overwrote without force")
	}
	b, _ := os.ReadFile(output)
	if string(b) != "old output" {
		t.Fatal("old output changed")
	}
	hash, err := Pack(context.Background(), plan, true, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(output, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dest := filepath.Join(t.TempDir(), "stage")
	if err := a.Extract(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dest, "empty")); err != nil || !info.IsDir() {
		t.Fatal("empty directory lost", err)
	}
	stagedHash, err := HashDirectory(context.Background(), dest, DefaultLimits())
	if err != nil || stagedHash != hash {
		t.Fatal("hash changed", hash, stagedHash, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	// Repacking remains byte-content deterministic and never includes its old output.
	secondPlan, err := Plan(context.Background(), source, output, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := Pack(context.Background(), secondPlan, true, DefaultLimits())
	if err != nil || secondHash != hash {
		t.Fatal(secondHash, err)
	}
}
func TestDirectoryRejectsEscapingLink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	os.WriteFile(outside, []byte("{}"), 0o644)
	if err := os.Symlink(outside, filepath.Join(root, "link.json")); err != nil {
		t.Skip(err)
	}
	d, err := OpenDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if f, err := d.Open("link.json"); err == nil {
		f.Close()
		t.Fatal("escaped root")
	}
}
func TestCompressionRatioAndReparseEntries(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "compressed.dspk")
	file, _ := os.Create(filename)
	w := zip.NewWriter(file)
	for name, data := range map[string]string{"desc.json": desc, "resource": strings.Repeat("0", 8192)} {
		entry, _ := w.Create(name)
		entry.Write([]byte(data))
	}
	w.Close()
	file.Close()
	limits := DefaultLimits()
	limits.CompressionRatio = 2
	if a, err := Open(filename, limits); err == nil {
		a.Close()
		t.Fatal("compression ratio ignored")
	}
	filename = zipFile(t, zipEntry{"desc.json", desc, 0})
	data, _ := os.ReadFile(filename)
	for i := 0; i+46 < len(data); i++ {
		if string(data[i:i+4]) == "PK\x01\x02" {
			binary.LittleEndian.PutUint16(data[i+4:i+6], 20)
			binary.LittleEndian.PutUint32(data[i+38:i+42], 0x400)
			break
		}
	}
	os.WriteFile(filename, data, 0o644)
	if a, err := Open(filename, DefaultLimits()); err == nil {
		a.Close()
		t.Fatal("reparse attribute accepted")
	}
}
func TestHashCompatibilityVector(t *testing.T) {
	filename := zipFile(t, zipEntry{"desc.json", "{}", 0})
	a, err := Open(filename, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	hash, err := a.Hash(context.Background())
	const expected = "c17d2dc3c264a1011d01dc0904e1204b6085510ceca2e3d08a7f3e8f548e3eb7a7296eefe90939c7a7e9a56210cab5f0a4303c2c1be59f20c0bcabb761dbaf29"
	if err != nil || hash != expected {
		t.Fatal(hash, err)
	}
}
