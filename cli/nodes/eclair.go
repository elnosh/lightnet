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

// EclairP2PContainerPort is the P2P port eclair always listens on inside the container.
const EclairP2PContainerPort = 9735

// EclairAPIContainerPort is the HTTP API port eclair always listens on inside the container.
const EclairAPIContainerPort = 8080

// EclairAPIPassword is the fixed password for eclair's HTTP API (basic auth, empty user).
const EclairAPIPassword = "lightnet"

// Tuning values below the bitcoind block are the ones Polar uses for regtest
// interop: regtest feerates differ wildly between implementations, so without
// the wide feerate tolerance eclair force-closes channels with LND/CLN.
// eclair subscribes to bitcoind's hashblock topic, not rawblock.
const eclairConfTemplate = `eclair {
  node-alias = "%s"
  chain = "regtest"

  server {
    binding-ip = "0.0.0.0"
    port = 9735
  }

  api {
    enabled = true
    binding-ip = "0.0.0.0"
    port = 8080
    password = "lightnet"
  }

  bitcoind {
    host = "%s"
    rpcport = %d
    rpcuser = "lightnet"
    rpcpassword = "lightnet"
    zmqblock = "tcp://%s:28334"
    zmqtx = "tcp://%s:28333"
    wallet = "%s"
  }

  on-chain-fees.feerate-tolerance {
    ratio-low = 0.00001
    ratio-high = 10000.0
  }
  channel {
    max-htlc-value-in-flight-percent = 100
    max-htlc-value-in-flight-msat = 5000000000000
    min-final-expiry-delta-blocks = 22
    fulfill-safety-before-timeout-blocks = 20
  }
  features.keysend = optional
}
`

// EclairNode implements Node for an eclair container.
type EclairNode struct {
	Name          string
	ContainerName string
}

func (n *EclairNode) Kind() string { return "eclair" }

func eclairCLIBase() []string {
	return []string{
		"eclair-cli",
		"-p", EclairAPIPassword,
		"-a", fmt.Sprintf("localhost:%d", EclairAPIContainerPort),
	}
}

func (n *EclairNode) BuildCommand(userArgs []string) []string {
	return append(eclairCLIBase(), userArgs...)
}

// EclairWalletName returns the name of the bitcoind wallet backing an eclair
// node. eclair has no wallet of its own and refuses to pick one when several
// are loaded, so each node gets a dedicated wallet named after it.
func EclairWalletName(nodeName string) string {
	return "eclair-" + nodeName
}

// GenerateEclairConfig writes eclair.conf and returns the data directory for bind-mounting.
func GenerateEclairConfig(networkName, nodeName, bitcoindContainer string, bitcoindRPCPort int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	dataDir := filepath.Join(home, ".lightnet", "networks", networkName, nodeName, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("creating eclair data dir: %w", err)
	}

	conf := fmt.Sprintf(eclairConfTemplate,
		nodeName,
		bitcoindContainer, bitcoindRPCPort,
		bitcoindContainer,
		bitcoindContainer,
		EclairWalletName(nodeName),
	)

	confPath := filepath.Join(dataDir, "eclair.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		return "", fmt.Errorf("writing eclair.conf: %w", err)
	}

	return dataDir, nil
}

// eclair-cli pipes curl through jq, so it can exit 0 with empty output while
// the API is still down; the output must actually parse to count as success.
func eclairGetInfo(ctx context.Context, c *client.Client, containerName string) (string, error) {
	out, err := dockerpkg.ExecOutput(ctx, c, containerName, append(eclairCLIBase(), "getinfo"))
	if err != nil {
		return "", err
	}
	var info struct {
		NodeID string `json:"nodeId"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("parsing eclair-cli getinfo: %w", err)
	}
	if info.NodeID == "" {
		return "", fmt.Errorf("eclair-cli getinfo returned no nodeId: %s", out)
	}
	return info.NodeID, nil
}

// FetchEclairPubkey returns the node's identity pubkey via eclair-cli getinfo.
func FetchEclairPubkey(ctx context.Context, c *client.Client, containerName string) (string, error) {
	return eclairGetInfo(ctx, c, containerName)
}

// WaitEclairReady needs nothing beyond getinfo: eclair only starts its API
// after it has connected to bitcoind and finished its startup checks.
func WaitEclairReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration) error {
	return pollUntilReady(ctx, c, containerName, timeout, "eclair", func(ctx context.Context) error {
		_, err := eclairGetInfo(ctx, c, containerName)
		return err
	})
}
