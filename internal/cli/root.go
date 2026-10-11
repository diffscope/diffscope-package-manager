package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/diffscope/diffscope-package-manager/internal/cli/commands"
	"github.com/diffscope/diffscope-package-manager/internal/cli/output"
	"github.com/diffscope/diffscope-package-manager/internal/config"
	"github.com/diffscope/diffscope-package-manager/internal/language"
	"github.com/diffscope/diffscope-package-manager/internal/packagemanager"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strconv"
)

var version = "0.0.0+dev"

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
	root.Flags().BoolVarP(&showVersion, "version", "v", false, "show version")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			if o.JSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"name": "dspm", "version": version})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "dspm version", version)
			return err
		}
		return cmd.Help()
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().StringVar(&o.PackagesDir, "packages-dir", o.PackagesDir, "package installation root")
	root.PersistentFlags().BoolVar(&o.JSON, "json", o.JSON, "emit structured JSON")
	root.PersistentFlags().StringVar(&o.Language, "language", o.Language, "language preference for text output; '*' shows all languages (JSON retains all languages)")
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
	name := cmd.Name()
	mutation := name == "install" || name == "remove" || name == "pack"
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
