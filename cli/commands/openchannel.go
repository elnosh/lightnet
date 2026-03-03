package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/moby/client"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/nodes"
	"github.com/elnosh/lightnet/cli/state"
)

// fundingMultiplier is the fraction of the channel amount sent to the node
// when auto-funding (1.5x gives headroom for fees and future channels).
const fundingMultiplier = 1.5

func RunOpenChannel(networkName, fromName, toName string, amountSats int64) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	fromState, ok := net.Nodes[fromName]
	if !ok {
		return fmt.Errorf("node %q not found in network %q", fromName, networkName)
	}
	toState, ok := net.Nodes[toName]
	if !ok {
		return fmt.Errorf("node %q not found in network %q", toName, networkName)
	}
	if toState.Connection.Pubkey == "" {
		return fmt.Errorf("node %q has no pubkey (is it a Lightning node?)", toName)
	}
	if toState.Connection.P2PInternal == "" {
		return fmt.Errorf("node %q has no P2P address", toName)
	}

	fromNode, err := nodeFromState(fromName, fromState)
	if err != nil {
		return err
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	// Auto-fund if the node doesn't have enough confirmed onchain balance.
	needed := int64(float64(amountSats) * fundingMultiplier)
	balance, err := lnConfirmedBalance(ctx, c, fromNode, fromState)
	if err != nil {
		return fmt.Errorf("checking balance for %q: %w", fromName, err)
	}
	if balance < needed {
		addr, err := lnNewOnchainAddress(ctx, c, fromNode, fromState)
		if err != nil {
			return fmt.Errorf("getting deposit address for %q: %w", fromName, err)
		}
		fundBTC := float64(needed) / 1e8
		fmt.Printf("Funding %s with %.8f BTC...\n", fromName, fundBTC)
		if err := RunFund(networkName, addr, fundBTC); err != nil {
			return fmt.Errorf("funding %q: %w", fromName, err)
		}
	}

	switch fromState.Kind {
	case "lnd":
		connectTarget := fmt.Sprintf("%s@%s", toState.Connection.Pubkey, toState.Connection.P2PInternal)
		connectCmd := fromNode.BuildCommand([]string{"connect", connectTarget})
		_, connectErr := dockerpkg.ExecOutput(ctx, c, fromState.ContainerName, connectCmd)
		if connectErr != nil && !isAlreadyConnected(connectErr) {
			return fmt.Errorf("connect: %w", connectErr)
		}

		openCmd := fromNode.BuildCommand([]string{
			"openchannel",
			"--node_key=" + toState.Connection.Pubkey,
			"--local_amt=" + fmt.Sprintf("%d", amountSats),
		})
		return dockerpkg.Exec(ctx, c, fromState.ContainerName, openCmd)

	case "ldk":
		openCmd := fromNode.BuildCommand([]string{
			"open-channel",
			toState.Connection.Pubkey,
			toState.Connection.P2PInternal,
			fmt.Sprintf("%dsat", amountSats),
			"--announce-channel",
		})
		return dockerpkg.Exec(ctx, c, fromState.ContainerName, openCmd)

	default:
		return fmt.Errorf("openchannel not supported for node type %q", fromState.Kind)
	}
}

// lnConfirmedBalance returns the confirmed onchain balance in satoshis.
func lnConfirmedBalance(ctx context.Context, c *client.Client, node nodes.Node, ns state.NodeState) (int64, error) {
	switch ns.Kind {
	case "lnd":
		out, err := dockerpkg.ExecOutput(ctx, c, ns.ContainerName, node.BuildCommand([]string{"walletbalance"}))
		if err != nil {
			return 0, err
		}
		var resp struct {
			ConfirmedBalance string `json:"confirmed_balance"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			return 0, fmt.Errorf("parsing walletbalance: %w", err)
		}
		return strconv.ParseInt(resp.ConfirmedBalance, 10, 64)

	case "ldk":
		out, err := dockerpkg.ExecOutput(ctx, c, ns.ContainerName, node.BuildCommand([]string{"get-balances"}))
		if err != nil {
			return 0, err
		}
		var resp struct {
			SpendableOnchainBalanceSats int64 `json:"spendable_onchain_balance_sats"`
		}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			return 0, fmt.Errorf("parsing get-balances: %w", err)
		}
		return resp.SpendableOnchainBalanceSats, nil

	default:
		return 0, fmt.Errorf("balance check not supported for node type %q", ns.Kind)
	}
}

// lnNewOnchainAddress returns a fresh deposit address for an LN node.
func lnNewOnchainAddress(ctx context.Context, c *client.Client, node nodes.Node, ns state.NodeState) (string, error) {
	var cmd []string
	switch ns.Kind {
	case "lnd":
		cmd = node.BuildCommand([]string{"newaddress", "p2wkh"})
	case "ldk":
		cmd = node.BuildCommand([]string{"onchain-receive"})
	default:
		return "", fmt.Errorf("new address not supported for node type %q", ns.Kind)
	}

	out, err := dockerpkg.ExecOutput(ctx, c, ns.ContainerName, cmd)
	if err != nil {
		return "", err
	}
	var resp struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("parsing address response: %w", err)
	}
	if resp.Address == "" {
		return "", fmt.Errorf("empty address returned")
	}
	return resp.Address, nil
}

func isAlreadyConnected(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already connected")
}
