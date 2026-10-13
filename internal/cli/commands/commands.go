package commands

import (
	"context"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/cli/output"
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/spf13/cobra"
	"path/filepath"
	"time"
)

type Options struct {
	PackagesDir     string
	JSON            bool
	Language        string
	CacheDir        string
	ConfigFile      string
	Registries      []string
	Concurrency     int
	Offline         bool
	DownloadTimeout time.Duration
	DownloadRetries int
	Unrestricted    bool
	Progress        string
}

func renderer(cmd *cobra.Command, o *Options, mutation bool) *output.Renderer {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	r := &output.Renderer{Command: commandName(cmd), JSON: o.JSON, Language: o.Language, PackagesDir: o.PackagesDir, DryRun: dryRun, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), Mutation: mutation, ProgressMode: o.Progress}
	if o.Progress == "auto" || o.Progress == "" {
		r.EnableTerminal()
	}
	return r
}
func Add(root *cobra.Command, o *Options) {
	root.AddCommand(install(o), downloadPackages(o), remove(o), info(o), list(o), pack(o), repository(o), cache(o))
}
func downloadPackages(o *Options) *cobra.Command {
	var directory string
	var dry bool
	c := &cobra.Command{Use: "download <package>...", Short: "Package archive and dependency download", Args: cobra.MinimumNArgs(1)}
	c.Flags().StringVarP(&directory, "output", "o", ".", "Select the archive export directory.")
	c.Flags().BoolVar(&dry, "dry-run", false, "Preview archive acquisition without downloading or exporting files.")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, true)
		defer r.Close()
		settings, err := remoteSettings(cmd, o)
		if err != nil {
			return r.Finish(err)
		}
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		result, err := packagemanager.New(o.PackagesDir).Download(ctx, packagemanager.DownloadRequest{Targets: args, Directory: directory, DryRun: dry, Settings: settings, Unrestricted: o.Unrestricted}, func(e packagemanager.Event) {
			r.Observer(e)
			if r.WriteError() != nil {
				cancel()
			}
		})
		r.Diagnostics(result.Diagnostics)
		return r.Finish(err)
	}
	return c
}
func install(o *Options) *cobra.Command {
	var file, fileOnly, overwrite, dryRun bool
	c := &cobra.Command{Use: "install <package>...", Short: "Package installation", Args: cobra.MinimumNArgs(1)}
	c.Flags().BoolVar(&file, "file", false, "Install explicit local archive files and acquire missing dependencies remotely.")
	c.Flags().BoolVar(&fileOnly, "file-only", false, "Use only explicit archives and installed dependencies without network or download cache access.")
	c.Flags().BoolVar(&overwrite, "overwrite-existing", false, "Permit replacement of different contents under the same package identity.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and preview without installing.")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, true)
		defer r.Close()
		var result packagemanager.MutationResult
		var err error
		ctx, cancel := context.WithCancel(cmd.Context())
		defer cancel()
		observe := func(e packagemanager.Event) {
			r.Observer(e)
			if r.WriteError() != nil {
				cancel()
			}
		}
		if fileOnly {
			result, err = packagemanager.New(o.PackagesDir).Install(ctx, packagemanager.InstallRequest{Files: args, Overwrite: overwrite, DryRun: dryRun}, observe)
		} else {
			var settings config.Remote
			settings, err = remoteSettings(cmd, o)
			if err == nil {
				result, err = packagemanager.New(o.PackagesDir).InstallRemote(ctx, packagemanager.RemoteRequest{Targets: args, File: file, Overwrite: overwrite, DryRun: dryRun, Settings: settings, Unrestricted: o.Unrestricted}, observe)
			}
		}
		r.Diagnostics(result.Diagnostics)
		return r.Finish(err)
	}
	return c
}
func remove(o *Options) *cobra.Command {
	var cascade, ignore, dryRun bool
	c := &cobra.Command{Use: "remove <package>...", Aliases: []string{"rm", "uninstall"}, Short: "Installed package removal", Args: cobra.MinimumNArgs(1)}
	c.Flags().BoolVar(&cascade, "cascade", false, "Include dependent packages that would become invalid.")
	c.Flags().BoolVar(&ignore, "ignore-non-existent", false, "Ignore requested packages that are not installed.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and preview without removing packages.")
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
	var file, installed, versions bool
	c := &cobra.Command{Use: "info <package>", Short: "Package information", Args: cobra.ExactArgs(1)}
	c.Flags().BoolVar(&file, "file", false, "Read a local DSPK archive.")
	c.Flags().BoolVar(&installed, "installed", false, "Read an installed package or contribution.")
	c.Flags().BoolVar(&versions, "versions", false, "List available remote versions in descending order.")
	c.MarkFlagsMutuallyExclusive("file", "installed", "versions")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		r := renderer(cmd, o, false)
		defer r.Close()
		if !file && !installed {
			settings, err := remoteSettings(cmd, o)
			if err != nil {
				return r.Finish(err)
			}
			if versions {
				data, err := packagemanager.VersionsRemote(cmd.Context(), args[0], settings, o.Unrestricted)
				if err != nil {
					return r.Finish(err)
				}
				return r.Query(data, nil)
			}
			data, diagnostics, err := packagemanager.InfoRemote(cmd.Context(), args[0], settings, o.Unrestricted)
			if err != nil {
				return r.Finish(err)
			}
			return r.Query(data, diagnostics)
		}
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
	c := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "Installed package listing", Args: cobra.NoArgs}
	c.Flags().StringVar(&columns, "columns", "id,version", "Select comma-separated text table columns.")
	c.Flags().StringVar(&columns, "column", "id,version", "Select text table columns as an alias for --columns.")
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
	c := &cobra.Command{Use: "pack <directory>", Short: "DSPK archive generation", Args: cobra.ExactArgs(1)}
	c.Flags().StringVarP(&filename, "output", "o", "", "Specify the destination DSPK filename.")
	c.Flags().BoolVar(&force, "force", false, "Permit replacement of an existing output file.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and preview without writing.")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if filename == "" {
			absolute, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("The source path cannot be resolved: %w", err)
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
