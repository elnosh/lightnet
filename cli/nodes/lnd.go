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

// LNDP2PContainerPort is the P2P port LND always listens on inside the container.
// The host-mapped port (p2p_port in YAML) can differ per node.
const LNDP2PContainerPort = 9735

// lndConfTemplate generates lnd.conf.
// rpclisten/restlisten live in [Application Options], NOT [RPC]/[REST].
// noseedbackup=true lets LND auto-create the wallet so no manual unlock is needed.
// Placeholders: grpcPort, restPort, bitcoindContainer, bitcoindRPCPort, bitcoindContainer, bitcoindContainer.
const lndConfTemplate = `[Application Options]
rpclisten=0.0.0.0:%d
restlisten=0.0.0.0:%d
listen=0.0.0.0:9735
noseedbackup=true
debuglevel=debug
alias=%s
accept-keysend=true

[Bitcoin]
bitcoin.active=1
bitcoin.regtest=1
bitcoin.node=bitcoind

[Bitcoind]
bitcoind.rpchost=%s:%d
bitcoind.rpcuser=lightnet
bitcoind.rpcpass=lightnet
bitcoind.zmqpubrawblock=tcp://%s:28332
bitcoind.zmqpubrawtx=tcp://%s:28333
`

// LNDNode implements Node for an LND container.
type LNDNode struct {
	Name          string
	ContainerName string
	// GRPCPort is the port LND's gRPC server listens on inside the container.
	// Needed so lncli can target the right port when run via docker exec.
	GRPCPort int
}

func (n *LNDNode) Kind() string { return "lnd" }

func (n *LNDNode) BuildCommand(userArgs []string) []string {
	// lncli may run as root inside the container, so explicitly point it at the
	// lnd user's data directory and gRPC server address rather than relying on
	// defaults ($HOME/.lnd and localhost:10009).
	base := []string{
		"lncli",
		fmt.Sprintf("--rpcserver=localhost:%d", n.GRPCPort),
		"--network=regtest",
		"--tlscertpath=/home/lnd/.lnd/tls.cert",
		"--macaroonpath=/home/lnd/.lnd/data/chain/bitcoin/regtest/admin.macaroon",
	}
	return append(base, userArgs...)
}

// GenerateLNDConfig writes lnd.conf and returns the data directory path for bind-mounting.
func GenerateLNDConfig(networkName, nodeName, bitcoindContainer string, bitcoindRPCPort, grpcPort, restPort int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	nodeDir := filepath.Join(home, ".lightnet", "networks", networkName, nodeName)
	dataDir := filepath.Join(nodeDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("creating lnd data dir: %w", err)
	}

	conf := fmt.Sprintf(lndConfTemplate,
		grpcPort,
		restPort,
		nodeName,
		bitcoindContainer, bitcoindRPCPort,
		bitcoindContainer,
		bitcoindContainer,
	)

	confPath := filepath.Join(dataDir, "lnd.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o644); err != nil {
		return "", fmt.Errorf("writing lnd.conf: %w", err)
	}

	return dataDir, nil
}

// FetchLNDPubkey returns the node's identity pubkey via lncli getinfo.
func FetchLNDPubkey(ctx context.Context, c *client.Client, containerName string, grpcPort int) (string, error) {
	cmd := []string{
		"lncli",
		fmt.Sprintf("--rpcserver=localhost:%d", grpcPort),
		"--network=regtest",
		"--tlscertpath=/home/lnd/.lnd/tls.cert",
		"--macaroonpath=/home/lnd/.lnd/data/chain/bitcoin/regtest/admin.macaroon",
		"getinfo",
	}
	out, err := dockerpkg.ExecOutput(ctx, c, containerName, cmd)
	if err != nil {
		return "", err
	}
	var info struct {
		IdentityPubkey string `json:"identity_pubkey"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", fmt.Errorf("parsing lncli getinfo: %w", err)
	}
	return info.IdentityPubkey, nil
}

// WaitLNDReady polls lncli listchannels until LND's server is fully ready.
// getinfo and listpeers succeed too early (before the P2P/routing subsystems
// finish starting). listchannels is gated behind server.Started() so it only
// succeeds once the full server is up.
// grpcPort is the port LND listens on inside the container.
func WaitLNDReady(ctx context.Context, c *client.Client, containerName string, grpcPort int, timeout time.Duration) error {
	cmd := []string{
		"lncli",
		fmt.Sprintf("--rpcserver=localhost:%d", grpcPort),
		"--network=regtest",
		"--tlscertpath=/home/lnd/.lnd/tls.cert",
		"--macaroonpath=/home/lnd/.lnd/data/chain/bitcoin/regtest/admin.macaroon",
		"listchannels",
	}
	return pollUntilReady(ctx, c, containerName, timeout, cmd, "lnd")
}
