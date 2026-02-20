package commands

import (
	"context"
	"fmt"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/state"
)

// RunStop stops all containers in a network.
func RunStop(networkName string, remove bool) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	for nodeName, n := range net.Nodes {
		fmt.Printf("Stopping %s (%s)...\n", nodeName, n.ContainerName)
		if err := dockerpkg.StopContainer(ctx, c, n.ContainerName); err != nil {
			fmt.Printf("  warning: %v\n", err)
		}
		if remove {
			if err := dockerpkg.RemoveContainer(ctx, c, n.ContainerName); err != nil {
				fmt.Printf("  warning: %v\n", err)
			}
		}
	}

	if remove {
		fmt.Printf("Removing Docker network %s...\n", net.DockerNetwork)
		if err := dockerpkg.RemoveNetwork(ctx, c, networkName); err != nil {
			fmt.Printf("  warning: %v\n", err)
		}
		if err := state.RemoveNetwork(networkName); err != nil {
			return fmt.Errorf("removing network from state: %w", err)
		}
		fmt.Printf("Network %q removed.\n", networkName)
	} else {
		if err := state.UpdateNetworkStatus(networkName, "stopped"); err != nil {
			return fmt.Errorf("updating state: %w", err)
		}
		fmt.Printf("Network %q stopped. Use --remove to also remove containers.\n", networkName)
	}

	return nil
}
