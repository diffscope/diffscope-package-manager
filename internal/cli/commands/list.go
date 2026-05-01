package commands

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewListCmd creates the list command.
func NewListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed packages",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("list not implemented")
		},
	}
}
