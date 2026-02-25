package docker

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"
)

// NetworkName returns the Docker bridge network name for a lightnet network.
func NetworkName(networkName string) string {
	return "lightnet-" + networkName
}

// CreateNetwork creates a Docker bridge network for the given lightnet network name.
// If the network already exists it is a no-op.
func CreateNetwork(ctx context.Context, c *client.Client, networkName string) error {
	name := NetworkName(networkName)
	_, err := c.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver: "bridge",
	})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("creating docker network %q: %w", name, err)
	}
	return nil
}

// RemoveNetwork removes the Docker bridge network for the given lightnet network name.
func RemoveNetwork(ctx context.Context, c *client.Client, networkName string) error {
	name := NetworkName(networkName)
	if _, err := c.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
		return fmt.Errorf("removing docker network %q: %w", name, err)
	}
	return nil
}
