package docker

import (
	"context"
	"fmt"
	"net/netip"
	"os"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// ContainerName returns the Docker container name for a node.
func ContainerName(networkName, nodeName string) string {
	return "lightnet-" + networkName + "-" + nodeName
}

// PortBinding is a host port → container port mapping.
type PortBinding struct {
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp" or "udp"
}

// VolumeMount maps a host directory path to a container path.
type VolumeMount struct {
	HostPath      string
	ContainerPath string
}

// CreateContainerOptions bundles all options for creating a container.
type CreateContainerOptions struct {
	Name        string
	Image       string
	NetworkName string
	Cmd         []string
	Ports       []PortBinding
	Mounts      []VolumeMount
	Env         []string
}

// CreateContainer creates (but does not start) a container, attached to the
// given lightnet bridge network.
func CreateContainer(ctx context.Context, c *client.Client, opts CreateContainerOptions) (string, error) {
	portBindings := network.PortMap{}
	exposedPorts := network.PortSet{}

	for _, pb := range opts.Ports {
		proto := pb.Protocol
		if proto == "" {
			proto = "tcp"
		}
		portStr := fmt.Sprintf("%d/%s", pb.ContainerPort, proto)
		p, err := network.ParsePort(portStr)
		if err != nil {
			return "", fmt.Errorf("parsing port %q: %w", portStr, err)
		}
		exposedPorts[p] = struct{}{}
		portBindings[p] = []network.PortBinding{
			{
				HostIP:   netip.MustParseAddr("127.0.0.1"),
				HostPort: fmt.Sprintf("%d", pb.HostPort),
			},
		}
	}

	var mounts []mount.Mount
	for _, m := range opts.Mounts {
		if err := os.MkdirAll(m.HostPath, 0o755); err != nil {
			return "", fmt.Errorf("creating host dir %q: %w", m.HostPath, err)
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: m.HostPath,
			Target: m.ContainerPath,
		})
	}

	// Attach directly to the lightnet network at create time so the container
	// never touches the default bridge and Docker DNS works immediately.
	resp, err := c.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name:  opts.Name,
		Image: opts.Image,
		Config: &container.Config{
			Cmd:          opts.Cmd,
			Env:          opts.Env,
			ExposedPorts: exposedPorts,
		},
		HostConfig: &container.HostConfig{
			PortBindings: portBindings,
			Mounts:       mounts,
			NetworkMode:  container.NetworkMode(opts.NetworkName),
		},
		NetworkingConfig: &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				opts.NetworkName: {},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating container %q: %w", opts.Name, err)
	}

	return resp.ID, nil
}

// StartContainer starts an already-created container.
func StartContainer(ctx context.Context, c *client.Client, containerID string) error {
	if _, err := c.ContainerStart(ctx, containerID, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("starting container %q: %w", containerID, err)
	}
	return nil
}

// StopContainer stops a running container gracefully.
func StopContainer(ctx context.Context, c *client.Client, name string) error {
	if _, err := c.ContainerStop(ctx, name, client.ContainerStopOptions{}); err != nil {
		return fmt.Errorf("stopping container %q: %w", name, err)
	}
	return nil
}

// RemoveContainer removes a stopped container.
func RemoveContainer(ctx context.Context, c *client.Client, name string) error {
	if _, err := c.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container %q: %w", name, err)
	}
	return nil
}
