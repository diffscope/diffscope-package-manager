package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/buildinfo"
	"github.com/diffscope/diffscope-package-manager/internal/cli/commands"
	"github.com/diffscope/diffscope-package-manager/internal/cli/output"
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/language"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
)

func NewRootCommand() *cobra.Command {
	o := &commands.Options{PackagesDir: config.DefaultPackagesDir(), Language: language.CurrentCode()}
	if v := os.Getenv("DSPM_PACKAGES_DIR"); v != "" {
		o.PackagesDir = v
	}
	if v := os.Getenv("DSPM_JSON"); v != "" {
		o.JSON, _ = strconv.ParseBool(v)
	}
	var showVersion bool
	root := &cobra.Command{Use: "dspm", Short: "DiffScope package manager", SilenceErrors: true, SilenceUsage: true}
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "Display the executable version.")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			if o.JSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"name": "dspm", "version": buildinfo.Version()})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "dspm version", buildinfo.Version())
			return err
		}
		return cmd.Help()
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&o.PackagesDir, "packages-dir", o.PackagesDir, "Set the package installation root.")
	root.PersistentFlags().BoolVar(&o.JSON, "json", o.JSON, "Emit structured JSON output.")
	root.PersistentFlags().StringVar(&o.Language, "language", o.Language, "Select the text output language. '*' displays all languages. JSON retains all languages.")
	root.PersistentFlags().StringArrayVar(&o.Registries, "registry", nil, "Replace configured sources with ordered package base URLs. This flag is repeatable.")
	root.PersistentFlags().StringVar(&o.CacheDir, "cache-dir", "", "Set the download cache root.")
	root.PersistentFlags().StringVar(&o.ConfigFile, "config", "", "Select the user configuration file.")
	root.PersistentFlags().IntVar(&o.Concurrency, "download-concurrency", 4, "Set concurrent archive tasks to a value from 1 to 16.")
	root.PersistentFlags().BoolVar(&o.Offline, "offline", false, "Use cached repository metadata and archives without network requests.")
	root.PersistentFlags().DurationVar(&o.DownloadTimeout, "download-timeout", 2*time.Hour, "Set the total time limit for each archive acquisition.")
	root.PersistentFlags().IntVar(&o.DownloadRetries, "download-retries", 3, "Set the number of additional transient transfer attempts.")
	root.PersistentFlags().BoolVar(&o.Unrestricted, "allow-unrestricted-downloads", false, "Permit HTTP and all destination addresses for this invocation. TLS and content verification remain enabled.")
	root.PersistentFlags().StringVar(&o.Progress, "progress", "auto", "Select auto, plain, or none for text progress.")
	commands.Add(root, o)
	return root
}
func ExecuteCommand(root *cobra.Command) error {
	cmd, err := root.ExecuteC()
	if err == nil {
		return nil
	}
	var reported *output.ReportedError
	if errors.As(err, &reported) {
		return err
	}
	if cmd == nil {
		cmd = root
	}
	structured, _ := root.PersistentFlags().GetBool("json")
	name := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
	mutation := name == "install" || name == "download" || name == "remove" || name == "pack" || name == "repo create" || name == "cache clean"
	r := &output.Renderer{Command: name, JSON: structured, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(), Mutation: mutation}
	r.EnableTerminal()
	return r.Finish(&packagemanager.Error{Code: "INVALID_ARGUMENT", Message: err.Error(), Cause: err})
}
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	root := NewRootCommand()
	root.SetContext(ctx)
	return ExecuteCommand(root)
}
