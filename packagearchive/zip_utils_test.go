package packagearchive

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestIndexPackageZipFilesRejectsSymbolicLink(t *testing.T) {
	archive := openZipArchiveForTest(t, makeZipArchive(t, []zipTestFile{
		{name: "link", body: "target", mode: os.ModeSymlink | 0777},
	}))

	_, err := indexPackageZipFiles(archive)
	if err == nil {
		t.Fatal("indexPackageZipFiles() expected symbolic link error")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("indexPackageZipFiles() error = %v", err)
	}
}

func TestIndexPackageZipFilesRejectsEncryptedEntry(t *testing.T) {
	archive := openZipArchiveForTest(t, makeZipArchive(t, []zipTestFile{
		{name: "secret.txt", body: "secret", flags: zipFlagEncrypted},
	}))

	_, err := indexPackageZipFiles(archive)
	if err == nil {
		t.Fatal("indexPackageZipFiles() expected encrypted entry error")
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("indexPackageZipFiles() error = %v", err)
	}
}

func TestIndexPackageZipFilesRejectsDuplicateEntry(t *testing.T) {
	archive := openZipArchiveForTest(t, makeZipArchiveWithDuplicateEntryForTest(t))

	_, err := indexPackageZipFiles(archive)
	if err == nil {
		t.Fatal("indexPackageZipFiles() expected duplicate entry error")
	}
	if !strings.Contains(err.Error(), `appears more than once`) {
		t.Fatalf("indexPackageZipFiles() error = %v", err)
	}
}

type zipTestFile struct {
	name  string
	body  string
	mode  os.FileMode
	flags uint16
}

func makeZipArchive(t *testing.T, files []zipTestFile) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range files {
		header := &zip.FileHeader{
			Name:   file.name,
			Method: zip.Deflate,
			Flags:  file.flags,
		}
		if file.mode != 0 {
			header.SetMode(file.mode)
		}

		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader(%q) error = %v", file.name, err)
		}
		if _, err := io.WriteString(entry, file.body); err != nil {
			t.Fatalf("WriteString(%q) error = %v", file.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buffer.Bytes()
}

func makeZipArchiveWithDuplicateEntryForTest(t *testing.T) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for index := 0; index < 2; index++ {
		entry, err := writer.Create("desc.json")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := io.WriteString(entry, "{}"); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return buffer.Bytes()
}

func openZipArchiveForTest(t *testing.T, data []byte) *zip.Reader {
	t.Helper()

	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	return archive
}
