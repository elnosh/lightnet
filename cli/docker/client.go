package docker

import (
	"fmt"

	"github.com/moby/moby/client"
)

// NewClient creates a Docker client from environment variables / unix socket defaults.
func NewClient() (*client.Client, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}
	return c, nil
}
