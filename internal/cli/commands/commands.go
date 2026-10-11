package commands

import (
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/cli/output"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/spf13/cobra"
	"path/filepath"
)

type Options struct {
	PackagesDir string
	JSON        bool
	Language    string
}

func renderer(cmd *cobra.Command, o *Options, mutation bool) *output.Renderer {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	r := &output.Renderer{Command: cmd.Name(), JSON: o.JSON, Language: o.Language, PackagesDir: o.PackagesDir, DryRun: dryRun, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), Mutation: mutation}
	r.EnableTerminal()
	return r
}
func Add(root *cobra.Command, o *Options) {
	root.AddCommand(install(o), remove(o), info(o), list(o), pack(o))
}
func install(o *Options) *cobra.Command {
	var file, overwrite, dryRun bool
	c := &cobra.Command{Use: "install <package>...", Short: "Install packages (use --file for local DSPK archives)", Args: cobra.MinimumNArgs(1)}
	c.Flags().BoolVar(&file, "file", false, "install local archive files")
	c.Flags().BoolVar(&overwrite, "overwrite-existing", false, "replace different contents with the same package identity")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "validate and preview without installing")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if !file {
			panic("not implemented")
		}
		r := renderer(cmd, o, true)
		defer r.Close()
		result, err := packagemanager.New(o.PackagesDir).Install(cmd.Context(), packagemanager.InstallRequest{Files: args, Overwrite: overwrite, DryRun: dryRun}, r.Observer)
		r.Diagnostics(result.Diagnostics)
		return r.Finish(err)
	}
	return c
}
func remove(o *Options) *cobra.Command {
	var cascade, ignore, dryRun bool
	c := &cobra.Command{Use: "remove <package>...", Aliases: []string{"rm", "uninstall"}, Short: "Remove installed packages", Args: cobra.MinimumNArgs(1)}
	c.Flags().BoolVar(&cascade, "cascade", false, "also remove dependents that become invalid")
	c.Flags().BoolVar(&ignore, "ignore-non-existent", false, "skip packages that are not installed")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "validate and preview without removing")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, true)
		defer r.Close()
		result, err := packagemanager.New(o.PackagesDir).Remove(cmd.Context(), packagemanager.RemoveRequest{Targets: args, Cascade: cascade, IgnoreMissing: ignore, DryRun: dryRun}, r.Observer)
		r.Diagnostics(result.Diagnostics)
		return r.Finish(err)
	}
	return c
}
func info(o *Options) *cobra.Command {
	var file, installed bool
	c := &cobra.Command{Use: "info <package>", Short: "Inspect a package (--file or --installed for local sources)", Args: cobra.ExactArgs(1)}
	c.Flags().BoolVar(&file, "file", false, "inspect a local DSPK file")
	c.Flags().BoolVar(&installed, "installed", false, "inspect an installed package or contribution")
	c.MarkFlagsMutuallyExclusive("file", "installed")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if !file && !installed {
			panic("not implemented")
		}
		r := renderer(cmd, o, false)
		defer r.Close()
		m := packagemanager.New(o.PackagesDir)
		var data packagemanager.Info
		var diagnostics []string
		var err error
		if file {
			data, diagnostics, err = m.InfoFile(cmd.Context(), args[0])
		} else {
			data, diagnostics, err = m.InfoInstalled(cmd.Context(), args[0])
		}
		if err != nil {
			r.Diagnostics(diagnostics)
			return r.Finish(err)
		}
		return r.Query(data, diagnostics)
	}
	return c
}
func list(o *Options) *cobra.Command {
	var columns string
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List installed packages", Args: cobra.NoArgs}
	c.Flags().StringVar(&columns, "columns", "id,version", "comma-separated text table columns")
	c.Flags().StringVar(&columns, "column", "id,version", "alias for --columns")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, false)
		defer r.Close()
		cols, err := output.ParseColumns(columns)
		if err != nil {
			return r.Finish(&packagemanager.Error{Code: "INVALID_ARGUMENT", Message: err.Error()})
		}
		data, diagnostics, err := packagemanager.New(o.PackagesDir).List(cmd.Context())
		if err != nil {
			r.Diagnostics(diagnostics)
			return r.Finish(err)
		}
		if o.JSON {
			return r.Query(data, diagnostics)
		}
		r.Diagnostics(diagnostics)
		r.List(data, cols)
		return r.Finish(nil)
	}
	return c
}
func pack(o *Options) *cobra.Command {
	var filename string
	var force, dryRun bool
	c := &cobra.Command{Use: "pack <directory>", Short: "Pack a JSON package directory into a DSPK archive", Args: cobra.ExactArgs(1)}
	c.Flags().StringVarP(&filename, "output", "o", "", "destination DSPK filename")
	c.Flags().BoolVar(&force, "force", false, "replace an existing output file")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "validate and preview without writing")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if filename == "" {
			absolute, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("source: %w", err)
			}
			filename = filepath.Join(filepath.Dir(absolute), filepath.Base(absolute)+".dspk")
		}
		r := renderer(cmd, o, true)
		defer r.Close()
		plan, err := packagemanager.New(o.PackagesDir).Pack(cmd.Context(), args[0], filename, force, dryRun, r.Observer)
		r.Diagnostics(plan.Package.Diagnostics)
		return r.Finish(err)
	}
	return c
}
