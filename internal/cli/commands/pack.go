package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"diffscope-package-manager/packagearchive"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

type packEvent struct {
	Command string        `json:"command"`
	Event   string        `json:"event"`
	Data    any           `json:"data,omitempty"`
	Error   *inspectError `json:"error,omitempty"`
}

type packReporter interface {
	CheckStart(sourceDir string)
	CheckWarning(warning packagearchive.PackWarning)
	CheckOK(plan packagearchive.PackPlan)
	DryRun(plan packagearchive.PackPlan)
	PackStart(plan packagearchive.PackPlan)
	PackProgress(progress packagearchive.PackProgress)
	PackDone(plan packagearchive.PackPlan, size int64, hash string)
	Result(plan packagearchive.PackPlan, size int64, hash string)
	Error(code string, message string, err error) error
}

// NewPackCmd creates the pack command.
func NewPackCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Pack a directory into a package file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputFile, err := cmd.Flags().GetString("output")
			if err != nil {
				return err
			}
			descFormat, err := cmd.Flags().GetString("desc-format")
			if err != nil {
				return err
			}
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			return PackPackageDirectory(
				cmd.Context(),
				args[0],
				outputFile,
				descFormat,
				dryRun,
				viper.GetBool("json"),
				cmd.OutOrStdout(),
			)
		},
	}

	cmd.Flags().StringP("output", "o", "", "output package file path")
	cmd.Flags().String("desc-format", "", "package description format: json, toml, yml, or yaml")
	cmd.Flags().Bool("dry-run", false, "check and report package contents without creating a package")
	return cmd
}

// PackPackageDirectory is the command entrypoint for creating package archives.
func PackPackageDirectory(ctx context.Context, sourceDir string, outputFile string, descFormat string, dryRun bool, jsonOutput bool, out io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reporter := newPackReporter(jsonOutput, out)

	absoluteSourceDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return reporter.Error("IO_ERROR", fmt.Sprintf("resolve source directory: %v", err), err)
	}
	if outputFile == "" {
		outputFile = defaultPackageOutputFile(absoluteSourceDir)
	} else {
		outputFile, err = filepath.Abs(outputFile)
		if err != nil {
			return reporter.Error("IO_ERROR", fmt.Sprintf("resolve output file: %v", err), err)
		}
	}

	reporter.CheckStart(absoluteSourceDir)
	plan, err := packagearchive.PlanPackage(absoluteSourceDir, packagearchive.PackOptions{
		DescFormat: descFormat,
		OutputFile: outputFile,
	})
	if err != nil {
		return reporter.Error("SCHEMA_ERROR", err.Error(), err)
	}
	for _, warning := range plan.Warnings {
		reporter.CheckWarning(warning)
	}
	reporter.CheckOK(plan)
	if dryRun {
		reporter.DryRun(plan)
		return nil
	}

	reporter.PackStart(plan)
	if err := packagearchive.CreatePackage(ctx, plan, reporter.PackProgress); err != nil {
		return reporter.Error("IO_ERROR", err.Error(), err)
	}

	size, hash, err := packageFileSizeAndHash(plan.OutputFile)
	if err != nil {
		return reporter.Error("IO_ERROR", err.Error(), err)
	}
	reporter.PackDone(plan, size, hash)
	reporter.Result(plan, size, hash)
	return nil
}

func defaultPackageOutputFile(sourceDir string) string {
	base := filepath.Base(sourceDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "package"
	}
	cwd, err := os.Getwd()
	if err != nil {
		return base + ".dspk"
	}
	return filepath.Join(cwd, base+".dspk")
}

func packageFileSizeAndHash(packageFilePath string) (int64, string, error) {
	reader, err := openPackageFileReader(packageFilePath)
	if err != nil {
		return 0, "", err
	}
	defer reader.Close()

	hash, err := packagearchive.ComputePackageHash(reader)
	if err != nil {
		return 0, "", err
	}
	return reader.size, fmt.Sprintf("%x", hash), nil
}

func newPackReporter(jsonOutput bool, out io.Writer) packReporter {
	if jsonOutput {
		return &packJSONReporter{encoder: json.NewEncoder(out)}
	}
	return newPackTextReporter(out)
}

type packJSONReporter struct {
	encoder *json.Encoder
}

func (r *packJSONReporter) CheckStart(sourceDir string) {
	r.event("CHECK_START", map[string]any{"sourceDir": sourceDir})
}

func (r *packJSONReporter) CheckWarning(warning packagearchive.PackWarning) {
	r.event("CHECK_WARNING", packWarningData(warning))
}

func (r *packJSONReporter) CheckOK(plan packagearchive.PackPlan) {
	r.event("CHECK_OK", packPlanData(plan))
}

func (r *packJSONReporter) DryRun(plan packagearchive.PackPlan) {
	r.event("DRY_RUN", packPlanData(plan))
}

func (r *packJSONReporter) PackStart(plan packagearchive.PackPlan) {
	r.event("PACK_START", map[string]any{"outputFile": plan.OutputFile, "total": plan.TotalBytes})
}

func (r *packJSONReporter) PackProgress(progress packagearchive.PackProgress) {
	r.event("PACK_PROGRESS", map[string]any{"filePath": progress.FilePath, "current": progress.Current, "total": progress.Total})
}

func (r *packJSONReporter) PackDone(plan packagearchive.PackPlan, size int64, hash string) {
	r.event("PACK_DONE", packResultData(plan, size, hash))
}

func (r *packJSONReporter) Result(plan packagearchive.PackPlan, size int64, hash string) {
	r.event("RESULT", packResultData(plan, size, hash))
}

func (r *packJSONReporter) Error(code string, message string, err error) error {
	_ = r.encoder.Encode(packEvent{
		Command: "pack",
		Event:   "ERROR",
		Error: &inspectError{
			Code:    code,
			Message: message,
		},
	})
	return err
}

func (r *packJSONReporter) event(event string, data any) {
	_ = r.encoder.Encode(packEvent{Command: "pack", Event: event, Data: data})
}

type packTextReporter struct {
	out       io.Writer
	useLive   bool
	multi     *pterm.MultiPrinter
	bar       *pterm.ProgressbarPrinter
	title     io.Writer
	packLabel string
	mu        sync.Mutex
}

func newPackTextReporter(out io.Writer) *packTextReporter {
	return &packTextReporter{out: out, useLive: packCanUseLiveOutput(out)}
}

func packCanUseLiveOutput(out io.Writer) bool {
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func (r *packTextReporter) CheckStart(sourceDir string) {
	fmt.Fprintf(r.out, "Checking package directory: %s\n", sourceDir)
}

func (r *packTextReporter) CheckWarning(warning packagearchive.PackWarning) {
	fmt.Fprintf(r.out, "%s %s: %s\n", installWarnStyle.Render("!"), warning.Path, warning.Message)
}

func (r *packTextReporter) CheckOK(plan packagearchive.PackPlan) {
	fmt.Fprintf(
		r.out,
		"%s %s@%s (%d files, %d descriptions, %d warnings)\n",
		installOKStyle.Render("✓"),
		plan.PackageID,
		plan.Version.String(),
		len(plan.Files),
		plan.DescriptionCount,
		len(plan.Warnings),
	)
}

func (r *packTextReporter) DryRun(plan packagearchive.PackPlan) {
	fmt.Fprintf(r.out, "Output (dry run): %s\n", plan.OutputFile)
	if len(plan.Conversions) > 0 {
		fmt.Fprintln(r.out, "Converted descriptions:")
		for _, conversion := range plan.Conversions {
			fmt.Fprintf(r.out, "  %s -> %s\n", conversion.SourcePath, conversion.PackedPath)
		}
	}
}

func (r *packTextReporter) PackStart(plan packagearchive.PackPlan) {
	r.packLabel = fmt.Sprintf("%s@%s", plan.PackageID, plan.Version.String())
	if !r.useLive {
		fmt.Fprintf(r.out, "Packing: %s\n", plan.OutputFile)
		return
	}
	multi := pterm.DefaultMultiPrinter.WithWriter(r.out).WithUpdateDelay(100 * time.Millisecond)
	r.multi = multi
	title := multi.NewWriter()
	fmt.Fprintf(title, "%s: packing", installPackageStyle.Render(r.packLabel))
	bar, _ := pterm.DefaultProgressbar.
		WithWriter(multi.NewWriter()).
		WithTotal(1000).
		WithMaxWidth(0).
		WithShowElapsedTime(false).
		WithShowCount(false).
		WithShowTitle(false).
		WithTitle(r.packLabel + ": packing").
		Start()
	r.title = title
	r.bar = bar
	_, _ = multi.Start()
}

func (r *packTextReporter) PackProgress(progress packagearchive.PackProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useLive {
		if r.bar == nil {
			return
		}
		if r.title != nil && progress.FilePath != "" {
			fmt.Fprintf(r.title, "\r%s: packing %s", installPackageStyle.Render(r.packLabel), progress.FilePath)
		}
		if progress.Total > 0 {
			current := int((progress.Current * 1000) / progress.Total)
			if current > r.bar.Current {
				r.bar.Add(current - r.bar.Current)
			}
		}
		return
	}
	if progress.FilePath != "" {
		fmt.Fprintf(r.out, "packing %s (%d/%d)\n", progress.FilePath, progress.Current, progress.Total)
	}
}

func (r *packTextReporter) PackDone(plan packagearchive.PackPlan, size int64, hash string) {
	r.stopPackMulti()
	fmt.Fprintf(r.out, "%s packed %s@%s\n", installOKStyle.Render("✓"), plan.PackageID, plan.Version.String())
	fmt.Fprintf(r.out, "Output: %s\n", plan.OutputFile)
	fmt.Fprintf(r.out, "Files: %d\n", len(plan.Files))
	fmt.Fprintf(r.out, "Size: %d bytes\n", size)
	fmt.Fprintf(r.out, "Hash: %s\n", hash)
}

func (r *packTextReporter) Result(plan packagearchive.PackPlan, size int64, hash string) {
}

func (r *packTextReporter) Error(code string, message string, err error) error {
	r.stopPackMulti()
	fmt.Fprintln(r.out, installErrorStyle.Render(message))
	return err
}

func (r *packTextReporter) stopPackMulti() {
	if r.multi != nil && r.multi.IsActive {
		if r.bar != nil && r.bar.Current < r.bar.Total {
			r.bar.Add(r.bar.Total - r.bar.Current)
		}
		_, _ = r.multi.Stop()
	}
}

func packPlanData(plan packagearchive.PackPlan) map[string]any {
	conversions := make([]map[string]any, 0, len(plan.Conversions))
	for _, conversion := range plan.Conversions {
		conversions = append(conversions, map[string]any{"sourcePath": conversion.SourcePath, "packedPath": conversion.PackedPath})
	}
	warnings := make([]map[string]any, 0, len(plan.Warnings))
	for _, warning := range plan.Warnings {
		warnings = append(warnings, packWarningData(warning))
	}
	return map[string]any{
		"sourceDir":         plan.SourceDir,
		"outputFile":        plan.OutputFile,
		"id":                plan.PackageID,
		"version":           plan.Version.String(),
		"descriptionPath":   plan.DescriptionPath,
		"descriptionFormat": plan.DescriptionFormat,
		"descriptions":      plan.DescriptionCount,
		"inferences":        plan.InferenceCount,
		"singers":           plan.SingerCount,
		"files":             len(plan.Files),
		"total":             plan.TotalBytes,
		"convertedFiles":    plan.ConvertedFilesCount,
		"conversions":       conversions,
		"warnings":          warnings,
	}
}

func packResultData(plan packagearchive.PackPlan, size int64, hash string) map[string]any {
	data := packPlanData(plan)
	data["size"] = size
	data["hash"] = hash
	return data
}

func packWarningData(warning packagearchive.PackWarning) map[string]any {
	return map[string]any{"path": warning.Path, "message": warning.Message}
}
