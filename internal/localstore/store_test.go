package localstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/diffscope/diffscope-package-manager/internal/localstore"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"github.com/diffscope/diffscope-package-manager/packagearchive"
	"github.com/diffscope/diffscope-package-manager/packagedatabase"
)

func put(t *testing.T, s *localstore.Session, filename string) localstore.Put {
	t.Helper()
	a, err := packagearchive.Open(filename, packagearchive.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	stage, p, err := s.Stage(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := s.StagedHash(context.Background(), stage, packagearchive.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.LoadStage(stage, p.Identity)
	if err != nil {
		t.Fatal(err)
	}
	return localstore.Put{Record: packagedatabase.Index(p, hash, localstore.DirectoryName(p.Identity), time.Now().Unix()), Stage: stage}
}
func install(t *testing.T, root, filename string) {
	t.Helper()
	s, err := localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := put(t, s, filename)
	if err = s.Commit(context.Background(), nil, []localstore.Put{p}); err != nil {
		t.Fatal(err)
	}
}
func TestCrashHelper(t *testing.T) {
	if os.Getenv("DSPM_CRASH_HELPER") != "1" {
		return
	}
	root, action, point := os.Getenv("DSPM_TEST_ROOT"), os.Getenv("DSPM_TEST_ACTION"), os.Getenv("DSPM_TEST_POINT")
	if action == "lock-read" || action == "lock-write" {
		s, err := localstore.Open(root, action == "lock-write")
		expect := os.Getenv("DSPM_TEST_EXPECT")
		if err != nil {
			if expect == "denied" && strings.Contains(err.Error(), "locked") {
				return
			}
			t.Fatal(err)
		}
		s.Close()
		if expect == "denied" {
			t.Fatal("lock unexpectedly acquired")
		}
		return
	}
	s, err := localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, err := s.Records()
	if err != nil {
		t.Fatal(err)
	}
	var p []localstore.Put
	if action != "remove" {
		p = append(p, put(t, s, os.Getenv("DSPM_TEST_FILE")))
	}
	if point == "stage" {
		os.Exit(73)
	}
	s.Fault = func(name string) error {
		if name == point {
			os.Exit(73)
		}
		return nil
	}
	var remove []packagedatabase.Record
	if action != "install" {
		remove = records
	}
	if err := s.Commit(context.Background(), remove, p); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash checkpoint was not reached")
}
func child(root, action, point, filename, expect string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "DSPM_CRASH_HELPER=1", "DSPM_TEST_ROOT="+root, "DSPM_TEST_ACTION="+action, "DSPM_TEST_POINT="+point, "DSPM_TEST_FILE="+filename, "DSPM_TEST_EXPECT="+expect)
	return cmd
}
func crash(t *testing.T, root, action, point, filename string) {
	t.Helper()
	output, err := child(root, action, point, filename, "").CombinedOutput()
	if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 73 {
		t.Fatalf("crash helper: %s %v", output, err)
	}
}
func TestProcessInterruptionRecovery(t *testing.T) {
	for _, action := range []string{"install", "overwrite", "remove"} {
		for _, point := range []string{"stage", "journal", "backup", "promote", "before-commit", "committed", "cleanup"} {
			if action == "install" && point == "backup" || action == "remove" && (point == "promote" || point == "stage") {
				continue
			}
			t.Run(action+"/"+point, func(t *testing.T) {
				root := t.TempDir()
				old := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "old"})
				newFile := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "new"})
				if action != "install" {
					install(t, root, old)
				}
				crash(t, root, action, point, newFile)
				s, err := localstore.Open(root, false)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				records, err := s.Records()
				if err != nil {
					t.Fatal(err)
				}
				committed := point == "committed" || point == "cleanup"
				wantCount := 1
				if action == "install" && !committed || action == "remove" && committed {
					wantCount = 0
				}
				if len(records) != wantCount {
					t.Fatalf("records=%d want=%d", len(records), wantCount)
				}
				if len(records) > 0 {
					want := "old"
					if committed {
						want = "new"
					}
					content, err := os.ReadFile(filepath.Join(root, records[0].Directory, "resource"))
					if err != nil || string(content) != want {
						t.Fatal(string(content), want, err)
					}
				}
				entries, err := os.ReadDir(filepath.Join(root, ".dspm", "transactions"))
				if err != nil || len(entries) != 0 {
					t.Fatal("recovery materials not cleaned", entries, err)
				}
				if len(s.Diagnostics) == 0 {
					t.Fatal("recovery did not report maintenance")
				}
			})
		}
	}
}
func TestRollbackOnErrorAndCommittedCleanupWarning(t *testing.T) {
	root := t.TempDir()
	old := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "old"})
	newFile := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "new"})
	install(t, root, old)
	s, err := localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	records, _ := s.Records()
	p := put(t, s, newFile)
	s.Fault = func(point string) error {
		if point == "before-commit" {
			return fmt.Errorf("injected database failure")
		}
		return nil
	}
	if err = s.Commit(context.Background(), records, []localstore.Put{p}); err == nil {
		t.Fatal("fault ignored")
	}
	s.Close()
	s, err = localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	records, _ = s.Records()
	content, _ := os.ReadFile(filepath.Join(root, records[0].Directory, "resource"))
	if string(content) != "old" {
		t.Fatal("old data not restored")
	}
	p = put(t, s, newFile)
	s.Fault = func(point string) error {
		if point == "cleanup" {
			return fmt.Errorf("injected cleanup failure")
		}
		return nil
	}
	if err = s.Commit(context.Background(), records, []localstore.Put{p}); err != nil || len(s.Diagnostics) == 0 {
		t.Fatal("committed result misreported", err, s.Diagnostics)
	}
	s.Close()
	m := packagemanager.New(root)
	result, err := m.Install(context.Background(), packagemanager.InstallRequest{Files: []string{newFile}, DryRun: true}, nil)
	if err != nil || result.Actions[0].Action != "skip" || len(result.Diagnostics) == 0 {
		t.Fatal(result, err)
	}
}
func TestRecoveryStopsWhenOriginalIsMissing(t *testing.T) {
	root := t.TempDir()
	old := testutil.Package(t, "voice", "1", "1", nil, nil)
	install(t, root, old)
	crash(t, root, "remove", "backup", "")
	transactions := filepath.Join(root, ".dspm", "transactions")
	dirs, _ := os.ReadDir(transactions)
	backup := filepath.Join(transactions, dirs[0].Name(), "backup-0")
	// The test deliberately removes the backup within its verified temporary directory.
	relative, err := filepath.Rel(root, backup)
	if err != nil || !filepath.IsLocal(relative) {
		t.Fatal("unsafe fixture")
	}
	if err = os.RemoveAll(backup); err != nil {
		t.Fatal(err)
	}
	if s, err := localstore.Open(root, false); err == nil {
		s.Close()
		t.Fatal("guessed missing original")
	}
	if _, err := os.Stat(filepath.Join(transactions, dirs[0].Name(), "journal.json")); err != nil {
		t.Fatal("recovery evidence deleted")
	}
}
func TestCrossProcessLockProtocol(t *testing.T) {
	root := t.TempDir()
	s, err := localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"lock-read", "lock-write"} {
		if output, err := child(root, action, "", "", "denied").CombinedOutput(); err != nil {
			t.Fatalf("%s %v", output, err)
		}
	}
	s.Close()
	s, err = localstore.Open(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if output, err := child(root, "lock-read", "", "", "allowed").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", output, err)
	}
	if output, err := child(root, "lock-write", "", "", "denied").CombinedOutput(); err != nil {
		t.Fatalf("%s %v", output, err)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		if output, err := child(root, "lock-write", "", "", "allowed").CombinedOutput(); err != nil {
			t.Fatalf("%s %v", output, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".dspm", "lock")); err != nil {
		t.Fatal("lock file removed", err)
	}
}
func TestSQLFailureRollsBackWholeBatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	old := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "old"})
	changed := testutil.Package(t, "voice", "1", "1", nil, map[string]string{"resource": "new"})
	additional := testutil.Package(t, "base", "1", "1", nil, nil)
	install(t, root, old)
	s, err := localstore.Open(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	records, _ := s.Records()
	first, second := put(t, s, changed), put(t, s, additional)
	db, err := sql.Open("sqlite3", filepath.Join(root, ".dspm", "packages.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON packages BEGIN SELECT RAISE(ABORT, 'injected insert failure'); END`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, records, []localstore.Put{first, second}); err == nil || !strings.Contains(err.Error(), "injected insert failure") {
		t.Fatal(err)
	}
	after, err := s.Records()
	if err != nil || len(after) != 1 || after[0].Hash != records[0].Hash {
		t.Fatal(after, err)
	}
	content, err := os.ReadFile(filepath.Join(root, after[0].Directory, "resource"))
	if err != nil || string(content) != "old" {
		t.Fatal(string(content), err)
	}
	if _, err = os.Stat(filepath.Join(root, second.Record.Directory)); !os.IsNotExist(err) {
		t.Fatal("partial new package remains")
	}
}
func TestMissingDatabaseDoesNotGuessCommitState(t *testing.T) {
	root := t.TempDir()
	filename := testutil.Package(t, "voice", "1", "1", nil, nil)
	crash(t, root, "install", "committed", filename)
	database := filepath.Join(root, ".dspm", "packages.db")
	if err := os.Remove(database); err != nil {
		t.Fatal(err)
	}
	for _, write := range []bool{false, true} {
		if s, err := localstore.Open(root, write); err == nil {
			s.Close()
			t.Fatal("guessed commit state", write)
		}
		if _, err := os.Stat(database); !os.IsNotExist(err) {
			t.Fatal("recreated missing recovery database", err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".dspm", "transactions"))
	if err != nil || len(entries) != 1 {
		t.Fatal("lost recovery evidence", entries, err)
	}
}
