package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/elnosh/lightnet/cli/cmd"
	"github.com/elnosh/lightnet/cli/commands"
)

var knownCmds = map[string]bool{
	"start":   true,
	"stop":    true,
	"info":    true,
	"list":    true,
	"help":    true,
	"--help":  true,
	"-h":      true,
	"version": true,
}

func main() {
	args := os.Args[1:]
	if len(args) > 0 && !knownCmds[args[0]] && !strings.HasPrefix(args[0], "-") {
		// lightnet <network> <node> [cmd...]
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: lightnet <network> <node> [command...]")
			os.Exit(1)
		}
		if err := commands.RunNodeExec(args[0], args[1], args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
