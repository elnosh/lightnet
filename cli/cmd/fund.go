package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/elnosh/lightnet/cli/commands"
	"github.com/spf13/cobra"
)

var fundCmd = &cobra.Command{
	Use:   "fund <network> <address> <amount>",
	Short: "Send BTC to an address and mine 6 confirming blocks",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := strconv.ParseFloat(args[2], 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid amount %q: %v\n", args[2], err)
			os.Exit(1)
		}
		if err := commands.RunFund(args[0], args[1], amount); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}
