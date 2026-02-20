package nodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/moby/moby/client"
)

// CLNP2PContainerPort is the P2P port CLN always listens on inside the container.
const CLNP2PContainerPort = 9735

const clnConfTemplate = `network=regtest
bitcoin-rpcuser=lightnet
bitcoin-rpcpassword=lightnet
bitcoin-rpcconnect=%s
bitcoin-rpcport=%d
grpc-port=%d
bind-addr=0.0.0.0:9735
`

// CLNNode implements Node for a Core Lightning container.
type CLNNode struct {
	Name          string
	ContainerName string
}

func (n *CLNNode) Kind() string { return "cln" }

func (n *CLNNode) BuildCommand(userArgs []string) []string {
	base := []string{"lightning-cli", "--network=regtest"}
	return append(base, userArgs...)
}

// GenerateCLNConfig writes a CLN config file and returns the data directory for bind-mounting.
func GenerateCLNConfig(networkName, nodeName, bitcoindContainer string, bitcoindRPCPort, grpcPort int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	nodeDir := filepath.Join(home, ".lightnet", "networks", networkName, nodeName)
	dataDir := filepath.Join(nodeDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("creating cln data dir: %w", err)
	}

	conf := fmt.Sprintf(clnConfTemplate, bitcoindContainer, bitcoindRPCPort, grpcPort)

	confPath := filepath.Join(dataDir, "config")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		return "", fmt.Errorf("writing cln config: %w", err)
	}

	return dataDir, nil
}

// WaitCLNReady polls lightning-cli getinfo until CLN is ready.
func WaitCLNReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := []string{"lightning-cli", "--network=regtest", "getinfo"}

	for time.Now().Before(deadline) {
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := dockerpkg.ExecOutput(execCtx, c, containerName, cmd)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}

	return fmt.Errorf("cln %q did not become ready within %s", containerName, timeout)
}
