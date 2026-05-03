package commands

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const packageDescriptionFileName = "desc.json"

type contributionPaths struct {
	Inferences map[string]string
	Singers    map[string]string
}

type packageContributionPathsDescription struct {
	Contributes struct {
		Inferences []string `json:"inferences"`
		Singers    []string `json:"singers"`
	} `json:"contributes"`
}

type moduleIDDescription struct {
	ID string `json:"id"`
}

func readArchiveContributionPaths(reader packageFileReader) (contributionPaths, error) {
	archive, err := zip.NewReader(reader, reader.Size())
	if err != nil {
		return contributionPaths{}, fmt.Errorf("open package zip: %w", err)
	}

	readFile := func(filePath string) ([]byte, error) {
		normalizedPath := cleanPackageRelativePath(filePath)
		for _, file := range archive.File {
			if cleanPackageRelativePath(file.Name) != normalizedPath {
				continue
			}
			handle, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip entry %q: %w", normalizedPath, err)
			}
			defer handle.Close()
			data, err := io.ReadAll(handle)
			if err != nil {
				return nil, fmt.Errorf("read zip entry %q: %w", normalizedPath, err)
			}
			return data, nil
		}
		return nil, fmt.Errorf("zip entry %q not found", normalizedPath)
	}

	return readContributionPaths(readFile, cleanPackageRelativePath)
}

func readInstalledContributionPaths(packageDir string) (contributionPaths, error) {
	descriptionPath := filepath.Join(packageDir, packageDescriptionFileName)
	descriptionData, err := os.ReadFile(descriptionPath)
	if err != nil {
		if isMissingPathError(err) {
			return newContributionPaths(), nil
		}
		return contributionPaths{}, fmt.Errorf("read %s: %w", packageDescriptionFileName, err)
	}

	readFile := func(filePath string) ([]byte, error) {
		if cleanPackageRelativePath(filePath) == packageDescriptionFileName {
			return descriptionData, nil
		}
		absolutePath := filepath.Join(packageDir, filepath.FromSlash(cleanPackageRelativePath(filePath)))
		data, err := os.ReadFile(absolutePath)
		if err != nil {
			return nil, err
		}
		return data, nil
	}

	paths, err := readContributionPaths(readFile, func(filePath string) string {
		relativePath := cleanPackageRelativePath(filePath)
		if relativePath == "" {
			return ""
		}
		return filepath.Join(packageDir, filepath.FromSlash(relativePath))
	})
	return paths, err
}

func readContributionPaths(readFile func(string) ([]byte, error), displayPath func(string) string) (contributionPaths, error) {
	descriptionData, err := readFile(packageDescriptionFileName)
	if err != nil {
		return contributionPaths{}, fmt.Errorf("read %s: %w", packageDescriptionFileName, err)
	}

	var description packageContributionPathsDescription
	if err := json.Unmarshal(descriptionData, &description); err != nil {
		return contributionPaths{}, fmt.Errorf("parse %s: %w", packageDescriptionFileName, err)
	}

	paths := newContributionPaths()
	if err := addModuleContributionPaths(paths.Inferences, description.Contributes.Inferences, readFile, displayPath); err != nil {
		return contributionPaths{}, err
	}
	if err := addModuleContributionPaths(paths.Singers, description.Contributes.Singers, readFile, displayPath); err != nil {
		return contributionPaths{}, err
	}
	return paths, nil
}

func addModuleContributionPaths(paths map[string]string, filePaths []string, readFile func(string) ([]byte, error), displayPath func(string) string) error {
	for _, filePath := range filePaths {
		data, err := readFile(filePath)
		if err != nil {
			return fmt.Errorf("read contribution %q: %w", filePath, err)
		}

		var description moduleIDDescription
		if err := json.Unmarshal(data, &description); err != nil {
			return fmt.Errorf("parse contribution %q: %w", filePath, err)
		}
		if description.ID != "" {
			paths[description.ID] = displayPath(filePath)
		}
	}
	return nil
}

func newContributionPaths() contributionPaths {
	return contributionPaths{
		Inferences: make(map[string]string),
		Singers:    make(map[string]string),
	}
}

func cleanPackageRelativePath(value string) string {
	normalized := strings.ReplaceAll(value, "\\", "/")
	cleaned := path.Clean(normalized)
	if cleaned == "." {
		return ""
	}
	return cleaned
}

func isMissingPathError(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot find the path specified") ||
		strings.Contains(message, "no such file or directory")
}
