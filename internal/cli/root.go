package cli

import (
	"diffscope-package-manager/internal/cli/commands"
	"diffscope-package-manager/internal/config"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	version = "0.0.0+dev"
)

type versionJsonOutput struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

var (
	rootCmd = &cobra.Command{
		Use:           "dspm",
		Short:         "DiffScope package manager",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig(cmd)
		},
		RunE: func(cmd *cobra.Command, arg []string) error {
			showVersion, err := cmd.Flags().GetBool("version")
			if err != nil {
				return err
			}
			if showVersion {
				if loadedConfig.JSON {
					output := versionJsonOutput{
						Name:    cmd.Use,
						Version: version,
					}
					jsonBytes, err := json.Marshal(output)
					if err != nil {
						return err
					}
					fmt.Fprintln(cmd.OutOrStdout(), string(jsonBytes))
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "%s version %s\n", cmd.Use, version)
				}
				return nil
			}
			return cmd.Help()
		},
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	loadedConfig config.Config
)

// Execute runs the CLI entrypoint.
func Execute() error {
	return rootCmd.Execute()
}

// Config returns the loaded global config.
func Config() config.Config {
	return loadedConfig
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "output JSON")
	rootCmd.PersistentFlags().String("packages-dir", "", "packages installation directory")
	rootCmd.Flags().BoolP("version", "v", false, "version for "+rootCmd.Use)

	rootCmd.AddCommand(
		commands.NewInstallCmd(),
		commands.NewRemoveCmd(),
		commands.NewListCmd(),
		commands.NewInfoCmd(),
		commands.NewInspectCmd(),
		commands.NewPackCmd(),
	)
}

func initConfig(cmd *cobra.Command) error {
	viper.SetDefault("packages_dir", config.DefaultPackagesDir())
	viper.SetDefault("json", false)
	viper.SetEnvPrefix("DSPM")
	viper.AutomaticEnv()

	if err := viper.BindEnv("packages_dir", "DSPM_PACKAGES_DIR"); err != nil {
		return err
	}
	if err := viper.BindPFlag("packages_dir", cmd.Flags().Lookup("packages-dir")); err != nil {
		return err
	}
	if err := viper.BindPFlag("json", cmd.Flags().Lookup("json")); err != nil {
		return err
	}

	return viper.Unmarshal(&loadedConfig)
}
