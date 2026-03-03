package commands

import (
	"context"
	"fmt"
	"strings"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/state"
)

func RunCreateBlocks(networkName string, count int) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

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

	addrOut, err := dockerpkg.ExecOutput(ctx, c, btcContainer,
		append(rpcBase, "-rpcwallet=test", "getnewaddress", "", "bech32"))
	if err != nil {
		return fmt.Errorf("getnewaddress: %w", err)
	}
	miningAddr := strings.TrimSpace(addrOut)

	_, err = dockerpkg.ExecOutput(ctx, c, btcContainer,
		append(rpcBase, "generatetoaddress", fmt.Sprintf("%d", count), miningAddr))
	if err != nil {
		return fmt.Errorf("generatetoaddress: %w", err)
	}

	fmt.Printf("Mined %d blocks.\n", count)
	return nil
}
