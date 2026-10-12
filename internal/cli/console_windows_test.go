//go:build windows

package cli

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/diffscope/diffscope-package-manager/internal/testutil"
	"golang.org/x/sys/windows"
)

// Runs the CLI entry point with signal handling in a native Windows pseudoconsole.
func TestConsoleHelper(t *testing.T) {
	if os.Getenv("DSPM_CONSOLE_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"dspm"}, os.Args[i+1:]...)
			break
		}
	}
	_ = os.Setenv("TERM", "xterm-256color")
	_ = os.Setenv("NO_COLOR", "1")
	_ = os.Unsetenv("DSPM_JSON")
	// Native console behavior requires the attached console handles in place of
	// the test runner's inherited pipe handles.
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		os.Exit(91)
	}
	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		os.Exit(92)
	}
	os.Stdout, os.Stderr, os.Stdin = out, out, in
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(out.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(out.Fd()))
	_ = windows.SetStdHandle(windows.STD_INPUT_HANDLE, windows.Handle(in.Fd()))
	if executable := os.Getenv("DSPM_TEST_CLI_BINARY"); executable != "" {
		// The launcher supplies console handles to the binary as a terminal
		// shell does. The test runner's pipe handles remain outside the child.
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
		child := exec.Command(executable, os.Args[1:]...)
		child.Stdin, child.Stdout, child.Stderr = in, out, out
		if err := child.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				os.Exit(exit.ExitCode())
			}
			os.Exit(93)
		}
		os.Exit(0)
	}
	if err := Execute(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func consoleRun(t *testing.T, interruptAt string, args ...string) (string, uint32) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return consoleRunExecutable(t, executable, interruptAt, append([]string{"-test.run=^TestConsoleHelper$", "--"}, args...)...)
}

func consoleRunExecutable(t *testing.T, executable, interruptAt string, args ...string) (string, uint32) {
	t.Helper()
	var inputRead, inputWrite, outputRead, outputWrite windows.Handle
	for _, pair := range [][2]*windows.Handle{{&inputRead, &inputWrite}, {&outputRead, &outputWrite}} {
		if err := windows.CreatePipe(pair[0], pair[1], nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	input := os.NewFile(uintptr(inputWrite), "console-input")
	output := os.NewFile(uintptr(outputRead), "console-output")
	defer input.Close()
	defer output.Close()
	var console windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 100, Y: 30}, inputRead, outputWrite, 0, &console); err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(inputRead)
	windows.CloseHandle(outputWrite)
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attrs.Delete()
	// The PSEUDOCONSOLE attribute requires the handle value as lpValue.
	update := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
	ok, _, updateErr := update.Call(uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, uintptr(console), unsafe.Sizeof(console), 0, 0)
	if ok == 0 {
		windows.ClosePseudoConsole(console)
		t.Fatal(updateErr)
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	argv := append([]string{executable}, args...)
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = windows.EscapeArg(arg)
	}
	command, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		t.Fatal(err)
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcess(nil, command, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &si.StartupInfo, &process); err != nil {
		windows.ClosePseudoConsole(console)
		t.Fatal(err)
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	captured := make(chan []byte, 1)
	go func() {
		var data bytes.Buffer
		chunk := make([]byte, 4096)
		sent := false
		for {
			n, err := output.Read(chunk)
			if n > 0 {
				data.Write(chunk[:n])
				if interruptAt != "" && !sent && bytes.Contains(data.Bytes(), []byte(interruptAt)) {
					sent = true
					_, _ = input.Write([]byte{3})
				}
			}
			if err != nil {
				break
			}
		}
		captured <- data.Bytes()
	}()
	status, err := windows.WaitForSingleObject(process.Process, 30000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(process.Process, 99)
		windows.ClosePseudoConsole(console)
		t.Fatal("console child timed out", status, err)
	}
	var exit uint32
	if err := windows.GetExitCodeProcess(process.Process, &exit); err != nil {
		t.Fatal(err)
	}
	windows.ClosePseudoConsole(console)
	return string(<-captured), exit
}

func TestWindowsConsoleInstallPackAndInterrupt(t *testing.T) {
	t.Setenv("DSPM_CONSOLE_HELPER", "1")
	archive := testutil.Package(t, "console", "1", "1", nil, map[string]string{"resource.bin": strings.Repeat("x", 64<<20)})
	other := testutil.Package(t, "other", "1", "1", nil, map[string]string{"resource.bin": strings.Repeat("y", 1<<20)})
	root := filepath.Join(t.TempDir(), "packages")
	args := []string{"--packages-dir", root, "install", "--file", archive, other}
	output, exit := consoleRun(t, ": extracting", args...)
	if exit != 1 || !strings.Contains(output, "canceled") || strings.Contains(output, "[installed]") {
		t.Fatal("interrupt failed", exit, output)
	}
	if !strings.Contains(output, "\x1b[?25h") {
		t.Fatal("cursor not restored after interrupt", output)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".dspm" {
			t.Fatal("partial package survived cancellation", entry.Name())
		}
	}
	output, exit = consoleRun(t, "", args...)
	bar := regexp.MustCompile(`\d+%`)
	if exit != 0 || !bar.MatchString(output) || !strings.Contains(output, "█") || !strings.Contains(output, "console=1.0.0.0: extracting") || !strings.Contains(output, "other=1.0.0.0: extracting") || strings.Contains(output, "Verifying") || !strings.Contains(output, "2 installed") {
		t.Fatal("interactive install missing feedback", exit, output)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == ".dspm" {
			continue
		}
		packed := filepath.Join(t.TempDir(), "console.dspk")
		output, exit = consoleRun(t, "", "pack", filepath.Join(root, entry.Name()), "--output", packed)
		if exit != 0 || !bar.MatchString(output) || !strings.Contains(output, "█") || !strings.Contains(output, ": packing") || strings.Contains(output, "Verifying") || !strings.Contains(output, "Hash:") {
			t.Fatal("interactive pack missing feedback", exit, output)
		}
		if info, err := os.Stat(packed); err != nil || info.Size() == 0 {
			t.Fatal("archive was not published", err)
		}
	}
}
