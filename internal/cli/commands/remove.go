package commands

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewRemoveCmd creates the remove command.
func NewRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <package>...",
		Short: "Remove one or more packages",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("remove not implemented")
		},
	}

	cmd.Flags().Bool("cascade", false, "remove dependent packages recursively")
	return cmd
}
