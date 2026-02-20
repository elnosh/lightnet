package commands

import (
	"fmt"

	"github.com/elnosh/lightnet/cli/state"
)

// RunList prints all known networks from state.
func RunList() error {
	gs, err := state.Load()
	if err != nil {
		return err
	}

	if len(gs.Networks) == 0 {
		fmt.Println("No networks found.")
		return nil
	}

	fmt.Printf("%-20s  %-10s  %s\n", "NAME", "STATUS", "DOCKER NETWORK")
	fmt.Printf("%-20s  %-10s  %s\n", "----", "------", "--------------")
	for _, net := range gs.Networks {
		fmt.Printf("%-20s  %-10s  %s\n", net.Name, net.Status, net.DockerNetwork)
	}

	return nil
}
