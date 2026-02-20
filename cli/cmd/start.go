package cmd

import (
	"fmt"
	"os"

	"github.com/elnosh/lightnet/cli/commands"
	"github.com/spf13/cobra"
)

var rebuildImages bool

var startCmd = &cobra.Command{
	Use:   "start <network>",
	Short: "Start a network from a YAML file or network name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := commands.RunStart(args[0], rebuildImages); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	startCmd.Flags().BoolVar(&rebuildImages, "rebuild", false, "Rebuild images even if they already exist locally")
}
