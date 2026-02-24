package commands

import (
	"context"
	"fmt"
	"strings"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/state"
)

func RunFund(networkName, address string, amount float64) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	// Find the first bitcoind node (holds the test wallet with all funds)
	var btcContainer string
	for _, n := range net.Nodes {
		if n.Kind == "bitcoind" {
			btcContainer = n.ContainerName
			break
		}
	}
	if btcContainer == "" {
		return fmt.Errorf("network %q has no bitcoind node", networkName)
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	rpcBase := []string{"bitcoin-cli", "-regtest", "-rpcuser=lightnet", "-rpcpassword=lightnet"}

	// Send to address from the test wallet (funded with 101 blocks at startup)
	txidOut, err := dockerpkg.ExecOutput(ctx, c, btcContainer,
		append(rpcBase, "-rpcwallet=test", "sendtoaddress", address, fmt.Sprintf("%.8f", amount)))
	if err != nil {
		return fmt.Errorf("sendtoaddress: %w", err)
	}
	txid := strings.TrimSpace(txidOut)

	// Get a fresh address to mine the confirming blocks to (keeps funds in the test wallet)
	addrOut, err := dockerpkg.ExecOutput(ctx, c, btcContainer,
		append(rpcBase, "-rpcwallet=test", "getnewaddress", "", "bech32"))
	if err != nil {
		return fmt.Errorf("getnewaddress: %w", err)
	}
	miningAddr := strings.TrimSpace(addrOut)

	// Mine 6 confirming blocks (no -rpcwallet needed — destination is explicit)
	_, err = dockerpkg.ExecOutput(ctx, c, btcContainer,
		append(rpcBase, "generatetoaddress", "6", miningAddr))
	if err != nil {
		return fmt.Errorf("mining confirming blocks: %w", err)
	}

	fmt.Printf("Funded %s with %.8f BTC\n", address, amount)
	fmt.Printf("txid: %s\n", txid)
	fmt.Printf("6 confirming blocks mined.\n")
	return nil
}
