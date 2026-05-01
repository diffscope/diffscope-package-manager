package commands

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewInstallCmd creates the install command.
func NewInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <packageFile>...",
		Short: "Install one or more packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("install not implemented")
		},
	}

	cmd.Flags().Bool("overwrite-existing", false, "overwrite if a different package is already installed")
	return cmd
}
