package commands

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackPackageDirectoryDryRunDoesNotCreatePackage(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "desc.json"), []byte(`{
		"contributes": {
			"inferences": [],
			"singers": []
		},
		"dependencies": [],
		"id": "vendor/simple",
		"version": "1.0"
	}`), 0o644); err != nil {
		t.Fatalf("write desc.json: %v", err)
	}

	outputFile := filepath.Join(t.TempDir(), "simple.dspk")
	var output bytes.Buffer
	if err := PackPackageDirectory(context.Background(), sourceDir, outputFile, "", true, false, &output); err != nil {
		t.Fatalf("PackPackageDirectory() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Fatalf("dry run output file stat err = %v", err)
	}
	if !strings.Contains(output.String(), "(dry run)") {
		t.Fatalf("dry run output missing marker:\n%s", output.String())
	}
}

func TestPackPackageDirectoryCreatesPackage(t *testing.T) {
	sourceDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sourceDir, "desc.json"), []byte(`{
		"contributes": {
			"inferences": [],
			"singers": []
		},
		"dependencies": [],
		"id": "vendor/simple",
		"version": "1.0"
	}`), 0o644); err != nil {
		t.Fatalf("write desc.json: %v", err)
	}

	outputFile := filepath.Join(t.TempDir(), "simple.dspk")
	var output bytes.Buffer
	if err := PackPackageDirectory(context.Background(), sourceDir, outputFile, "", false, false, &output); err != nil {
		t.Fatalf("PackPackageDirectory() error = %v\n%s", err, output.String())
	}
	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("stat output package: %v", err)
	}
	if !strings.Contains(output.String(), "packed vendor/simple@1.0.0.0") {
		t.Fatalf("pack output missing result:\n%s", output.String())
	}
}
