package packagearchive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkProgress(t *testing.T, samples []Progress, total int64, files int) {
	t.Helper()
	if len(samples) < 4 {
		t.Fatal("no incremental progress", samples)
	}
	var previous int64
	intermediate := false
	for _, p := range samples {
		if p.Total != total || p.Current < previous || p.Current > total || p.FilesDone > files {
			t.Fatalf("invalid progress: %+v", p)
		}
		if p.FilePath == "resource.bin" && p.Current > 0 && p.Current < total-128<<10 {
			intermediate = true
		}
		previous = p.Current
	}
	last := samples[len(samples)-1]
	if last.Current != total || last.FilesDone != files || last.FilesTotal != files || !intermediate {
		t.Fatalf("incomplete progress: %+v, intermediate=%v", last, intermediate)
	}
}

func TestStreamingProgressAcrossArchiveOperations(t *testing.T) {
	source := t.TempDir()
	files := map[string]string{"desc.json": desc, "singer.json": singer, "resource.bin": strings.Repeat("x", 2<<20), "empty": ""}
	var total int64
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(source, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		total += int64(len(data))
	}
	ctx := context.Background()
	plan, err := Plan(ctx, source, filepath.Join(t.TempDir(), "test.dspk"), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var packed, verified, hashed, extracted, staged []Progress
	hash, err := PackWithProgress(ctx, plan, false, DefaultLimits(), func(p Progress) { packed = append(packed, p) }, func(p Progress) { verified = append(verified, p) })
	if err != nil {
		t.Fatal(err)
	}
	a, err := Open(plan.Output, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got, err := a.HashWithProgress(ctx, func(p Progress) { hashed = append(hashed, p) })
	if err != nil || got != hash {
		t.Fatal(got, err)
	}
	dest := filepath.Join(t.TempDir(), "extracted")
	if err := a.ExtractWithProgress(ctx, dest, func(p Progress) { extracted = append(extracted, p) }); err != nil {
		t.Fatal(err)
	}
	got, err = HashDirectoryWithProgress(ctx, dest, DefaultLimits(), func(p Progress) { staged = append(staged, p) })
	if err != nil || got != hash {
		t.Fatal(got, err)
	}
	for _, samples := range [][]Progress{packed, verified, hashed, extracted, staged} {
		checkProgress(t, samples, total, len(files))
	}
}

func TestProgressCancellationAndCorruption(t *testing.T) {
	filename := zipFile(t, zipEntry{"desc.json", desc, 0}, zipEntry{"resource.bin", strings.Repeat("PAYLOAD", 1<<18), 0})
	a, err := Open(filename, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var last Progress
	err = a.ExtractWithProgress(ctx, filepath.Join(t.TempDir(), "stage"), func(p Progress) {
		last = p
		if p.Current > 128<<10 {
			cancel()
		}
	})
	a.Close()
	if !errors.Is(err, context.Canceled) || last.Current >= last.Total || last.FilesDone >= last.FilesTotal {
		t.Fatal("cancellation falsely completed", last, err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	data[strings.Index(string(data), "PAYLOAD")] = 'X'
	if err := os.WriteFile(filename, data, 0o644); err != nil {
		t.Fatal(err)
	}
	a, err = Open(filename, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	_, err = a.HashWithProgress(context.Background(), func(p Progress) { last = p })
	if err == nil || last.FilesDone == last.FilesTotal {
		t.Fatal("corrupt file reported completed", last, err)
	}
}
