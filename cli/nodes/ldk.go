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

// LDKRESTContainerPort is the REST port ldk-server listens on inside the container.
const LDKRESTContainerPort = 3002

// LDKP2PContainerPort is the Lightning P2P port ldk-server listens on inside the container.
const LDKP2PContainerPort = 3001

const ldkConfTemplate = `[node]
network = "regtest"                           # Bitcoin network to use
listening_addresses = ["0.0.0.0:%d"]        # Lightning node listening addresses
#announcement_addresses = ["54.3.7.81:3001"] # Lightning node announcement addresses
rest_service_address = "0.0.0.0:3002"       # LDK Server REST address
alias = "test"                               # Lightning node alias

# Storage settings
[storage.disk]
# dir_path = "/tmp/ldk-server/"                 # Path for LDK and BDK data persistence, optional, defaults to ~/.ldk-server/

# [log]
# level = "Debug"                               # Log level (Error, Warn, Info, Debug, Trace)
# file = "/tmp/ldk-server/ldk-server.log"       # Log file path

[tls]
#cert_path = "/path/to/tls.crt"               # Path to TLS certificate, by default uses dir_path/tls.crt
#key_path = "/path/to/tls.key"                # Path to TLS private key, by default uses dir_path/tls.key

[bitcoind]
rpc_address = "%s:%d"               # RPC endpoint
rpc_user = "lightnet"                        # RPC username
rpc_password = "lightnet"                    # RPC password
`

// ldkCLIBase is the base command for interacting with ldk-server inside the
// container. ldk-cli is a thin wrapper that resolves the binary api_key from
// /data/regtest/api_key (hex-encoding it for ldk-server-cli's --api-key flag)
// and sets the correct --base-url and --tls-cert paths.
var ldkCLIBase = []string{"ldk-cli"}

// LDKNode implements Node for an ldk-server container.
type LDKNode struct {
	Name          string
	ContainerName string
}

func (n *LDKNode) Kind() string { return "ldk" }

func (n *LDKNode) BuildCommand(userArgs []string) []string {
	return append(append([]string{}, ldkCLIBase...), userArgs...)
}

// GenerateLDKConfig creates the data directory for an ldk-server node and
// returns the host path for bind-mounting. Configuration is passed via
// environment variables at container start time.
func GenerateLDKConfig(networkName, nodeName, bitcoindContainer string, bitcoindRPCPort, p2pPort int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dataDir := filepath.Join(home, ".lightnet", "networks", networkName, nodeName, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("creating ldk data dir: %w", err)
	}

	conf := fmt.Sprintf(ldkConfTemplate,
		p2pPort,
		bitcoindContainer,
		bitcoindRPCPort,
	)

	confPath := filepath.Join(dataDir, "ldk-config.toml")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		return "", fmt.Errorf("writing ldk-config.toml: %w", err)
	}

	return dataDir, nil
}

// WaitLDKReady polls ldk-server-cli get-node-info until the server is ready.
func WaitLDKReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := append(append([]string{}, ldkCLIBase...), "get-node-info")

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

	return fmt.Errorf("ldk-server %q did not become ready within %s", containerName, timeout)
}
