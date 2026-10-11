package packagemanager

import (
	"context"
	"errors"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalWorkflow(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "packages")
	m := New(root)
	base1 := testutil.Package(t, "base", "1", "1", nil, nil)
	base2 := testutil.Package(t, "base", "2", "1", nil, nil)
	voice := testutil.Package(t, "vendor/voice", "1", "1", []map[string]string{{"id": "base", "version": "1"}}, map[string]string{"singer.json": `{"name":{"_":"","zh-CN":"歌手"},"interface":"org.example.Voice","level":1,"variant":"v","avatar":"../../missing.png","demoAudio":5,"imports":false}`})
	preview, err := m.Install(ctx, InstallRequest{Files: []string{voice, base1}, DryRun: true}, nil)
	if err != nil || len(preview.Actions) != 2 {
		t.Fatal(preview, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("dry-run created store")
	}
	if _, err = m.Install(ctx, InstallRequest{Files: []string{voice}}, nil); err == nil {
		t.Fatal("missing dependency accepted")
	}
	installed, err := m.Install(ctx, InstallRequest{Files: []string{voice, base1, base2}}, nil)
	if err != nil || len(installed.Actions) != 3 {
		t.Fatal(installed, err)
	}
	info, _, err := m.InfoInstalled(ctx, "vendor/voice=1:singer/main")
	if err != nil || info.Package.Contributions[0].Singer.Avatar["_"] != "../../missing.png" || info.Package.Contributions[0].Name["_"] != "" || info.Package.Contributions[0].Name["zh-CN"] != "歌手" {
		t.Fatal(info, err)
	}
	repeat, err := m.Install(ctx, InstallRequest{Files: []string{voice}}, nil)
	if err != nil || repeat.Actions[0].Action != "skip" {
		t.Fatal(repeat, err)
	}
	if _, _, err = m.InfoInstalled(ctx, "base"); err == nil {
		t.Fatal("ambiguous version accepted")
	}
	if _, err = m.Remove(ctx, RemoveRequest{Targets: []string{"base=2"}}, nil); err != nil {
		t.Fatal("failed to reselect base=1", err)
	}
	if _, err = m.Remove(ctx, RemoveRequest{Targets: []string{"base=1"}}, nil); err == nil {
		t.Fatal("removed required dependency")
	}
	if _, err = m.Remove(ctx, RemoveRequest{Targets: []string{"base=1"}, Cascade: true, DryRun: true}, nil); err != nil {
		t.Fatal(err)
	}
	records, _, _ := m.List(ctx)
	if len(records) != 2 {
		t.Fatal("dry-run removed packages")
	}
	removed, err := m.Remove(ctx, RemoveRequest{Targets: []string{"base=1"}, Cascade: true}, nil)
	if err != nil || len(removed.Actions) != 2 {
		t.Fatal(removed, err)
	}
	records, _, err = m.List(ctx)
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
}
func TestOverwriteAndFinalGraph(t *testing.T) {
	ctx := context.Background()
	m := New(t.TempDir())
	a := testutil.Package(t, "a", "1", "1", nil, map[string]string{"resource": "old"})
	changed := testutil.Package(t, "a", "1", "1", nil, map[string]string{"resource": "new"})
	if _, err := m.Install(ctx, InstallRequest{Files: []string{a}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, InstallRequest{Files: []string{changed}}, nil); err == nil {
		t.Fatal("silent overwrite")
	}
	result, err := m.Install(ctx, InstallRequest{Files: []string{changed}, Overwrite: true}, nil)
	if err != nil || result.Actions[0].Action != "overwrite" {
		t.Fatal(result, err)
	}
	bad := testutil.Package(t, "a", "1", "1", []map[string]string{{"id": "missing", "version": "1"}}, nil)
	if _, err := m.Install(ctx, InstallRequest{Files: []string{bad}, Overwrite: true}, nil); err == nil {
		t.Fatal("invalid overwrite accepted")
	}
	info, _, err := m.InfoInstalled(ctx, "a=1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(m.Root, info.Installation.Directory, "resource"))
	if err != nil || string(b) != "new" {
		t.Fatal(string(b), err)
	}
	// Installing a higher compatible version may introduce a cycle for an existing root.
	b1 := testutil.Package(t, "b", "1", "1", nil, nil)
	root := testutil.Package(t, "root", "1", "1", []map[string]string{{"id": "b", "version": "1"}}, nil)
	if _, err = m.Install(ctx, InstallRequest{Files: []string{b1, root}}, nil); err != nil {
		t.Fatal(err)
	}
	b2 := testutil.Package(t, "b", "2", "1", []map[string]string{{"id": "root", "version": "1"}}, nil)
	if _, err = m.Install(ctx, InstallRequest{Files: []string{b2}}, nil); err == nil {
		t.Fatal("new version broke existing graph")
	}
}
func TestUnregisteredPackagesAreIgnored(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "manual"), 0o755)
	os.WriteFile(filepath.Join(root, "manual", "desc.json"), []byte(`{}`), 0o644)
	m := New(root)
	records, _, err := m.List(context.Background())
	if err != nil || len(records) != 0 {
		t.Fatal(records, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dspm")); !os.IsNotExist(err) {
		t.Fatal("query initialized store")
	}
	_, _, err = m.InfoInstalled(context.Background(), "manual")
	var e *Error
	if !errors.As(err, &e) || e.Code != "NOT_FOUND" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".dspm")); !os.IsNotExist(err) {
		t.Fatal("info initialized store")
	}
}
func TestPackDryRunDetectsOutputConflict(t *testing.T) {
	source := t.TempDir()
	os.WriteFile(filepath.Join(source, "desc.json"), []byte(`{"$version":"1","runtimeLevel":1,"id":"a","version":"1"}`), 0o644)
	output := filepath.Join(t.TempDir(), "existing.dspk")
	os.WriteFile(output, []byte("old"), 0o644)
	m := New(t.TempDir())
	if _, err := m.Pack(context.Background(), source, output, false, true, nil); err == nil {
		t.Fatal("dry-run ignored output conflict")
	}
	if _, err := m.Pack(context.Background(), source, output, true, true, nil); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(output)
	if string(content) != "old" {
		t.Fatal("dry-run overwrote output")
	}
}
