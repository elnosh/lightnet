package nodes

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/moby/moby/client"
)

// Bitcoin Core requires network-specific settings (rpcbind, rpcallowip, zmq*)
// to live inside the [regtest] section when regtest=1 is active.
const bitcoinConfTemplate = `regtest=1

[regtest]
server=1
rpcuser=lightnet
rpcpassword=lightnet
rpcallowip=0.0.0.0/0
rpcbind=0.0.0.0
rpcport=18443
listen=1
listenonion=0
txindex=1
zmqpubrawblock=tcp://0.0.0.0:28332
zmqpubrawtx=tcp://0.0.0.0:28333
fallbackfee=0.0002
`

// BitcoindNode implements Node for a bitcoind container.
type BitcoindNode struct {
	Name          string
	ContainerName string
}

func (b *BitcoindNode) Kind() string { return "bitcoind" }

func (b *BitcoindNode) BuildCommand(userArgs []string) []string {
	base := []string{
		"bitcoin-cli",
		"-regtest",
		"-rpcuser=lightnet",
		"-rpcpassword=lightnet",
	}
	return append(base, userArgs...)
}

// GenerateBitcoindConfig writes bitcoin.conf to the node's config directory and
// returns the host directory path for bind-mounting.
func GenerateBitcoindConfig(networkName, nodeName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	nodeDir := filepath.Join(home, ".lightnet", "networks", networkName, nodeName)
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		return "", fmt.Errorf("creating node dir: %w", err)
	}

	dataDir := filepath.Join(nodeDir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("creating data dir: %w", err)
	}

	confPath := filepath.Join(dataDir, "bitcoin.conf")
	if err := os.WriteFile(confPath, []byte(bitcoinConfTemplate), 0o644); err != nil {
		return "", fmt.Errorf("writing bitcoin.conf: %w", err)
	}

	return dataDir, nil
}

// MineInitialBlocks creates a mining wallet, gets an address, and mines numBlocks
// blocks so that bitcoind exits initial block download (IBD) mode.
// This must be called before starting lightning nodes: LND/CLN wait on
// "chain backend finish sync" and will never complete if bitcoind stays in IBD.
func MineInitialBlocks(ctx context.Context, c *client.Client, containerName string, numBlocks int) error {
	rpcBase := []string{"bitcoin-cli", "-regtest", "-rpcuser=lightnet", "-rpcpassword=lightnet"}

	// Create the mining wallet; if it already exists, load it.
	_, err := dockerpkg.ExecOutput(ctx, c, containerName, append(rpcBase, "createwallet", "mining"))
	if err != nil {
		// Wallet may already exist — try loading it (ignore errors; it may be loaded).
		dockerpkg.ExecOutput(ctx, c, containerName, append(rpcBase, "loadwallet", "mining")) //nolint:errcheck
	}

	// Get a fresh address from the mining wallet.
	addrOut, err := dockerpkg.ExecOutput(ctx, c, containerName,
		append(rpcBase, "-rpcwallet=mining", "getnewaddress", "", "bech32"))
	if err != nil {
		return fmt.Errorf("getting mining address: %w", err)
	}
	address := strings.TrimSpace(addrOut)

	// Mine the blocks.
	_, err = dockerpkg.ExecOutput(ctx, c, containerName,
		append(rpcBase, "generatetoaddress", fmt.Sprintf("%d", numBlocks), address))
	if err != nil {
		return fmt.Errorf("mining %d blocks: %w", numBlocks, err)
	}
	return nil
}

// WaitUntilReady polls bitcoin-cli getblockchaininfo until it succeeds or times out.
func WaitUntilReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	cmd := []string{
		"bitcoin-cli", "-regtest",
		"-rpcuser=lightnet", "-rpcpassword=lightnet",
		"getblockchaininfo",
	}

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

	return fmt.Errorf("bitcoind %q did not become ready within %s", containerName, timeout)
}
