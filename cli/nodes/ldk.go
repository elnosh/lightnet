package nodes

import "fmt"

// LDKNode implements Node for an LDK-server container (stub — no image yet).
type LDKNode struct {
	Name          string
	ContainerName string
}

func (n *LDKNode) Kind() string { return "ldk" }

func (n *LDKNode) BuildCommand(userArgs []string) []string {
	// TODO: determine CLI tool for ldk-server when an image is available.
	return append([]string{"ldk-server"}, userArgs...)
}

// GenerateLDKConfig is a stub; returns an error until an image is available.
func GenerateLDKConfig(networkName, nodeName string) (string, error) {
	return "", fmt.Errorf("ldk-server: no Docker image available yet; stub only")
}
