package packagearchive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

const zipFlagEncrypted = 1 << 0

// ZipReader is the random-access input required to read a zip archive.
type ZipReader interface {
	io.ReaderAt
	Size() int64
}

type packageZipFiles map[string]*zip.File

func openPackageZip(reader ZipReader) (*zip.Reader, error) {
	archive, err := zip.NewReader(reader, reader.Size())
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	return archive, nil
}

func indexPackageZipFiles(archive *zip.Reader) (packageZipFiles, error) {
	files := make(packageZipFiles, len(archive.File))
	for _, file := range archive.File {
		if err := validateZipFile(file); err != nil {
			return nil, err
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if _, ok := files[file.Name]; ok {
			return nil, fmt.Errorf("zip entry %q appears more than once", file.Name)
		}
		files[file.Name] = file
	}
	return files, nil
}

func readPackageZipFile(files packageZipFiles, filePath string) ([]byte, error) {
	file, err := packageZipFileByPath(files, filePath)
	if err != nil {
		return nil, err
	}

	stream, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %q: %w", file.Name, err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %q: %w", file.Name, err)
	}
	return data, nil
}

func packageZipFileByPath(files packageZipFiles, filePath string) (*zip.File, error) {
	cleaned, err := cleanPackagePath(filePath)
	if err != nil {
		return nil, err
	}
	file, ok := files[cleaned]
	if !ok {
		return nil, fmt.Errorf("zip entry %q not found", cleaned)
	}
	return file, nil
}

func cleanPackagePath(filePath string) (string, error) {
	if filePath == "" {
		return "", fmt.Errorf("package path cannot be empty")
	}
	if strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "\\") {
		return "", fmt.Errorf("package path %q must be relative", filePath)
	}
	if strings.Contains(filePath, "\\") {
		return "", fmt.Errorf("package path %q must use forward slashes", filePath)
	}
	if strings.Contains(path.Clean(filePath), ":") {
		return "", fmt.Errorf("package path %q must not contain a volume name", filePath)
	}

	cleaned := path.Clean(filePath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("package path %q escapes package root", filePath)
	}
	return cleaned, nil
}

func validateZipFile(file *zip.File) error {
	if !utf8.ValidString(file.Name) {
		return fmt.Errorf("zip entry name is not valid UTF-8")
	}
	if file.Flags&zipFlagEncrypted != 0 {
		return fmt.Errorf("zip entry %q is encrypted", file.Name)
	}
	if file.FileInfo().Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("zip entry %q is a symbolic link", file.Name)
	}
	return nil
}
