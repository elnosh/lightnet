package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/moby/moby/client"
)

// CLNP2PContainerPort is the P2P port CLN always listens on inside the container.
const CLNP2PContainerPort = 9735

// clnLightningDir is where lightningd keeps its data inside the container.
// docker exec runs as root, so lightning-cli must be pointed here explicitly
// instead of defaulting to $HOME/.lightning.
const clnLightningDir = "/home/clightning/.lightning"

// grpc-host defaults to 127.0.0.1, which would make the host-mapped gRPC
// port unreachable; bind to all interfaces inside the container.
const clnConfTemplate = `network=regtest
bitcoin-rpcuser=lightnet
bitcoin-rpcpassword=lightnet
bitcoin-rpcconnect=%s
bitcoin-rpcport=%d
grpc-host=0.0.0.0
grpc-port=%d
bind-addr=0.0.0.0:9735
`

// CLNNode implements Node for a Core Lightning container.
type CLNNode struct {
	Name          string
	ContainerName string
}

func (n *CLNNode) Kind() string { return "cln" }

func clnCLIBase() []string {
	return []string{"lightning-cli", "--network=regtest", "--lightning-dir=" + clnLightningDir}
}

func (n *CLNNode) BuildCommand(userArgs []string) []string {
	return append(clnCLIBase(), userArgs...)
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

type clnGetInfo struct {
	ID                    string `json:"id"`
	WarningBitcoindSync   string `json:"warning_bitcoind_sync"`
	WarningLightningdSync string `json:"warning_lightningd_sync"`
}

func clnGetinfo(ctx context.Context, c *client.Client, containerName string) (*clnGetInfo, error) {
	out, err := dockerpkg.ExecOutput(ctx, c, containerName, append(clnCLIBase(), "getinfo"))
	if err != nil {
		return nil, err
	}
	var info clnGetInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return nil, fmt.Errorf("parsing lightning-cli getinfo: %w", err)
	}
	return &info, nil
}

// FetchCLNPubkey returns the node's identity pubkey via lightning-cli getinfo.
func FetchCLNPubkey(ctx context.Context, c *client.Client, containerName string) (string, error) {
	info, err := clnGetinfo(ctx, c, containerName)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

// WaitCLNReady polls lightning-cli getinfo until CLN is ready.
// getinfo answers as soon as the RPC socket is up, while lightningd may still
// be syncing with bitcoind; it reports that via warning_bitcoind_sync /
// warning_lightningd_sync, so wait until both are gone.
func WaitCLNReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		info, err := clnGetinfo(execCtx, c, containerName)
		cancel()
		if err == nil && info.WarningBitcoindSync == "" && info.WarningLightningdSync == "" {
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
