package packagearchive

import (
	"context"
	"crypto/sha512"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractionHashMatchesArchiveAndDirectory(t *testing.T) {
	file := zipFile(t, zipEntry{"z/data", "payload", 0}, zipEntry{"empty/", "", os.ModeDir}, zipEntry{"desc.json", desc, 0}, zipEntry{"a/empty", "", 0})
	a, err := Open(file, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	expected, err := a.Hash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "stage")
	actual, err := a.ExtractAndHash(context.Background(), dest, nil)
	if err != nil || actual != expected {
		t.Fatal(actual, expected, err)
	}
	disk, err := HashDirectory(context.Background(), dest, DefaultLimits())
	if err != nil || disk != actual {
		t.Fatal(disk, actual, err)
	}
}

func TestExtractionHashRejectsCRCSizeAndCancellation(t *testing.T) {
	file := zipFile(t, zipEntry{"desc.json", desc, 0}, zipEntry{"z.bin", "PAYLOAD", 0})
	a, err := Open(file, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err = a.ExtractAndHash(ctx, filepath.Join(t.TempDir(), "cancel"), func(p Progress) {
		if p.Current > 0 {
			cancel()
		}
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	a.limits.TotalBytes = 1
	if _, err = a.ExtractAndHash(context.Background(), filepath.Join(t.TempDir(), "limit"), nil); err == nil {
		t.Fatal("The stream limit was ignored.")
	}
	a.Close()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	data[strings.Index(string(data), "PAYLOAD")] = 'X'
	if err = os.WriteFile(file, data, 0644); err != nil {
		t.Fatal(err)
	}
	a, err = Open(file, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.ExtractAndHash(context.Background(), filepath.Join(t.TempDir(), "crc"), nil); err == nil {
		t.Fatal("A CRC failure was accepted.")
	}
}

type shortExtractionWriter struct{}

func (shortExtractionWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestExtractionHashWriterRejectsShortWrites(t *testing.T) {
	n, err := copyContextWithProgress(context.Background(), io.MultiWriter(shortExtractionWriter{}, sha512.New()), strings.NewReader("payload"), 1024, nil)
	if !errors.Is(err, io.ErrShortWrite) || n != 6 {
		t.Fatal(n, err)
	}
}
