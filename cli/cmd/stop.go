package cmd

import (
	"fmt"
	"os"

	"github.com/elnosh/lightnet/cli/commands"
	"github.com/spf13/cobra"
)

var removeContainers bool

var stopCmd = &cobra.Command{
	Use:   "stop <network>",
	Short: "Stop a running network",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := commands.RunStop(args[0], removeContainers); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	stopCmd.Flags().BoolVar(&removeContainers, "remove", false, "Remove containers after stopping")
}
