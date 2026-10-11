// Package testutil constructs independent package fixtures for integration tests.
package testutil

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func Package(t *testing.T, id, version, compat string, dependencies []map[string]string, resources map[string]string) string {
	t.Helper()
	manifest := map[string]any{"$version": "1", "runtimeLevel": 1, "id": id, "version": version, "name": map[string]string{"_": id, "zh-CN": "中文名称"}}
	if compat != "" {
		manifest["compatVersion"] = compat
	}
	if dependencies != nil {
		manifest["dependencies"] = dependencies
	}
	if resources == nil {
		resources = map[string]string{}
	}
	if _, ok := resources["singer.json"]; ok {
		manifest["contributions"] = map[string]any{"singer": []map[string]string{{"id": "main", "path": "singer.json"}}}
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "package.dspk")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, data := range resources {
		w, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	w, err := writer.CreateHeader(&zip.FileHeader{Name: "desc.json", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	w.Write(b)
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}
