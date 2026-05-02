package packagearchive

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// ExtractProgress reports package extraction progress.
type ExtractProgress struct {
	FilePath string
	Current  int64
	Total    int64
}

// ExtractPackage extracts a package archive into destinationDir.
//
// destinationDir must not already exist. Regular files are extracted in
// parallel, and progress is reported after each write chunk.
func ExtractPackage(ctx context.Context, reader ZipReader, destinationDir string, progress func(ExtractProgress)) error {
	if destinationDir == "" {
		return fmt.Errorf("extract package: destination directory is required")
	}
	if _, err := os.Stat(destinationDir); err == nil {
		return fmt.Errorf("extract package: destination directory already exists: %s", destinationDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("extract package: stat destination directory: %w", err)
	}

	archive, err := openPackageZip(reader)
	if err != nil {
		return err
	}
	files, err := indexPackageZipFiles(archive)
	if err != nil {
		return err
	}

	entries := make([]extractEntry, 0, len(files))
	var total int64
	for _, file := range files {
		cleaned, err := cleanPackagePath(file.Name)
		if err != nil {
			return err
		}
		size := int64(file.UncompressedSize64)
		total += size
		entries = append(entries, extractEntry{
			filePath: cleaned,
			size:     size,
			file:     file,
		})
	}

	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return fmt.Errorf("extract package: create destination directory: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	workerCount := runtime.GOMAXPROCS(0)
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(entries) && len(entries) > 0 {
		workerCount = len(entries)
	}

	jobs := make(chan extractEntry)
	errs := make(chan error, 1)
	var current int64
	var wg sync.WaitGroup

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for entry := range jobs {
				if err := ctx.Err(); err != nil {
					reportExtractError(errs, err, cancel)
					return
				}
				if progress != nil {
					progress(ExtractProgress{
						FilePath: entry.filePath,
						Current:  atomic.LoadInt64(&current),
						Total:    total,
					})
				}
				if err := extractOneFile(ctx, destinationDir, entry, total, &current, progress); err != nil {
					reportExtractError(errs, err, cancel)
					return
				}
			}
		}()
	}

sendJobs:
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			break sendJobs
		case jobs <- entry:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errs:
		return err
	default:
	}
	return ctx.Err()
}

type extractEntry struct {
	filePath string
	size     int64
	file     zipFile
}

type zipFile interface {
	Open() (io.ReadCloser, error)
}

func extractOneFile(
	ctx context.Context,
	destinationDir string,
	entry extractEntry,
	total int64,
	current *int64,
	progress func(ExtractProgress),
) error {
	target, err := packageDestinationPath(destinationDir, entry.filePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("extract package: create directory for %q: %w", entry.filePath, err)
	}

	src, err := entry.file.Open()
	if err != nil {
		return fmt.Errorf("extract package: open zip entry %q: %w", entry.filePath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("extract package: create file %q: %w", entry.filePath, err)
	}
	defer dst.Close()

	buffer := make([]byte, 256*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			if _, err := dst.Write(buffer[:n]); err != nil {
				return fmt.Errorf("extract package: write file %q: %w", entry.filePath, err)
			}
			value := atomic.AddInt64(current, int64(n))
			if progress != nil {
				progress(ExtractProgress{
					FilePath: entry.filePath,
					Current:  value,
					Total:    total,
				})
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("extract package: read zip entry %q: %w", entry.filePath, readErr)
		}
	}
}

func packageDestinationPath(destinationDir string, packagePath string) (string, error) {
	relative := filepath.FromSlash(packagePath)
	target := filepath.Join(destinationDir, relative)
	cleanDestination, err := filepath.Abs(destinationDir)
	if err != nil {
		return "", fmt.Errorf("extract package: resolve destination directory: %w", err)
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("extract package: resolve destination path %q: %w", packagePath, err)
	}
	if cleanTarget != cleanDestination && !isPathWithin(cleanTarget, cleanDestination) {
		return "", fmt.Errorf("extract package: zip entry %q escapes destination directory", packagePath)
	}
	return cleanTarget, nil
}

func isPathWithin(path string, parent string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return relative != ".." && relative != "." && !startsWithDotDot(relative)
}

func startsWithDotDot(path string) bool {
	return len(path) >= 3 && path[:3] == ".."+string(filepath.Separator)
}

func reportExtractError(errs chan<- error, err error, cancel context.CancelFunc) {
	select {
	case errs <- err:
		cancel()
	default:
	}
}
