package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "lightnet",
	Short: "Spin up declarative Lightning Network node networks using Docker",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(listCmd)
}
