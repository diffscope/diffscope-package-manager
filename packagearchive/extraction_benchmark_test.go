package packagearchive

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkExtractionVerificationGiB(b *testing.B) {
	filename := filepath.Join(b.TempDir(), "model.dspk")
	f, err := os.Create(filename)
	if err != nil {
		b.Fatal(err)
	}
	z := zip.NewWriter(f)
	desc, err := z.CreateHeader(&zip.FileHeader{Name: "desc.json", Method: zip.Store})
	if err != nil {
		b.Fatal(err)
	}
	if _, err = desc.Write([]byte(`{"$version":"1","runtimeLevel":1,"id":"model","version":"1"}`)); err != nil {
		b.Fatal(err)
	}
	w, err := z.CreateHeader(&zip.FileHeader{Name: "model.bin", Method: zip.Store})
	if err != nil {
		b.Fatal(err)
	}
	chunk := make([]byte, 128<<10)
	for n := 0; n < 1<<13; n++ {
		if _, err = w.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
	if err = z.Close(); err != nil {
		b.Fatal(err)
	}
	if err = f.Close(); err != nil {
		b.Fatal(err)
	}
	a, err := Open(filename, DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	defer a.Close()
	parent := b.TempDir()
	b.SetBytes(1 << 30)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dest := filepath.Join(parent, fmt.Sprint(i))
		if _, err = a.ExtractAndHash(context.Background(), dest, nil); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		if err = os.RemoveAll(dest); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
