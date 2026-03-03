package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/elnosh/lightnet/cli/commands"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lightnet",
	Short: "Spin up declarative Lightning Network node networks using Docker",
}

func Execute() error {
	return rootCmd.Execute()
}

var (
	rebuildImages    bool
	removeContainers bool
)

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

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all known networks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := commands.RunList(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}

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

var createBlocksCmd = &cobra.Command{
	Use:   "createblocks <network> <count>",
	Short: "Mine N blocks using the bitcoind test wallet",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid count %q: %v\n", args[1], err)
			os.Exit(1)
		}
		if err := commands.RunCreateBlocks(args[0], n); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}

var openChannelCmd = &cobra.Command{
	Use:   "openchannel <network> <from> <to> <amount>",
	Short: "Open a Lightning channel between two nodes",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := strconv.ParseInt(args[3], 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid amount %q: %v\n", args[3], err)
			os.Exit(1)
		}
		if err := commands.RunOpenChannel(args[0], args[1], args[2], amount); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	startCmd.Flags().BoolVar(&rebuildImages, "rebuild", false, "Rebuild images even if they already exist locally")
	stopCmd.Flags().BoolVar(&removeContainers, "remove", false, "Remove containers after stopping")

	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(fundCmd)
	rootCmd.AddCommand(createBlocksCmd)
	rootCmd.AddCommand(openChannelCmd)
}
