package commands

import (
	"errors"

	"github.com/spf13/cobra"
)

// NewInfoCmd creates the info command.
func NewInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <target>",
		Short: "Show installed package or module details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("info not implemented")
		},
	}

	cmd.Flags().String("language", "", "language tag or '*' for all languages")
	return cmd
}
