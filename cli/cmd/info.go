package cmd

import (
	"fmt"
	"os"

	"github.com/elnosh/lightnet/cli/commands"
	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info <network> [node]",
	Short: "Show connection info for a network or specific node",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		node := ""
		if len(args) == 2 {
			node = args[1]
		}
		if err := commands.RunInfo(args[0], node); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}
