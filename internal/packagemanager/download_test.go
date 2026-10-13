package packagemanager

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
)

func TestDownloadExportsInstalledDependenciesWithoutStoreChanges(t *testing.T) {
	base := testutil.Package(t, "base", "2", "1", nil, nil)
	voice := testutil.Package(t, "vendor/voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, nil)
	server := testutil.ServeRepository(t, base, voice)
	settings := remoteSettings(t, server.Server.URL)
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "packages"))
	if _, err := manager.Install(ctx, InstallRequest{Files: []string{base}}, nil); err != nil {
		t.Fatal(err)
	}
	before, _, err := manager.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "exports")
	request := DownloadRequest{Targets: []string{"vendor/voice"}, Directory: destination, Settings: settings, Unrestricted: true}
	result, err := manager.Download(ctx, request, nil)
	if err != nil || len(result.Actions) != 2 {
		t.Fatal(result, err)
	}
	after, _, err := manager.List(ctx)
	if err != nil || len(after) != 1 || before[0].Hash != after[0].Hash || before[0].InstalledAt != after[0].InstalledAt {
		t.Fatal(before, after, err)
	}
	for _, action := range result.Actions {
		original := base
		if action.Package.ID == "vendor/voice" {
			original = voice
		}
		expected, _ := os.ReadFile(original)
		actual, err := os.ReadFile(action.File)
		if err != nil || !bytes.Equal(expected, actual) {
			t.Fatal(action, err)
		}
	}
	server.Server.Close()
	request.Settings.Offline = true
	repeated, err := manager.Download(ctx, request, nil)
	if err != nil || len(repeated.Actions) != 2 || repeated.Actions[0].Action != "skip" || repeated.Actions[1].Action != "skip" {
		t.Fatal(repeated, err)
	}
	target := filepath.Join(destination, "base", "2.0.0.0.dspk")
	if err = os.WriteFile(target, []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.Download(ctx, request, nil); err == nil || !strings.Contains(err.Error(), "different archive") {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(target); string(data) != "existing" {
		t.Fatal("An existing export was replaced.")
	}
}

func TestDownloadDryRunAndOfflineMissLeaveDestinationsAbsent(t *testing.T) {
	file := testutil.Package(t, "voice", "1", "1", nil, nil)
	server := testutil.ServeRepository(t, file)
	settings := remoteSettings(t, server.Server.URL)
	manager := New(filepath.Join(t.TempDir(), "packages"))
	directory := filepath.Join(t.TempDir(), "exports")
	request := DownloadRequest{Targets: []string{"voice"}, Directory: directory, DryRun: true, Settings: settings, Unrestricted: true}
	if _, err := manager.Download(context.Background(), request, nil); err != nil {
		t.Fatal(err)
	}
	request.DryRun = false
	request.Settings.Offline = true
	if _, err := manager.Download(context.Background(), request, nil); err == nil || !strings.Contains(err.Error(), "not cached") {
		t.Fatal(err)
	}
	if server.Count("/archives/voice/1.0.0.0") != 0 {
		t.Fatal("Offline acquisition issued an archive request.")
	}
	for _, p := range []string{manager.Root, directory} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatal(p, err)
		}
	}
}
