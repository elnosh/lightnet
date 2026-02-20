package commands

import (
	"context"
	"fmt"
	"net"
	"strconv"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/nodes"
	"github.com/elnosh/lightnet/cli/state"
)

// RunNodeExec executes a command on a node inside a running network.
// Usage: lightnet <network> <node> [cmd...]
func RunNodeExec(networkName, nodeName string, userArgs []string) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	nodeState, ok := net.Nodes[nodeName]
	if !ok {
		return fmt.Errorf("node %q not found in network %q", nodeName, networkName)
	}

	node, err := nodeFromState(nodeName, nodeState)
	if err != nil {
		return err
	}

	cmd := node.BuildCommand(userArgs)

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}

	return dockerpkg.Exec(context.Background(), c, nodeState.ContainerName, cmd)
}

// nodeFromState constructs the appropriate Node implementation from stored state.
func nodeFromState(name string, ns state.NodeState) (nodes.Node, error) {
	switch ns.Kind {
	case "bitcoind":
		return &nodes.BitcoindNode{Name: name, ContainerName: ns.ContainerName}, nil
	case "lnd":
		// Extract the container-internal gRPC port from the stored GRPCUrl
		// (e.g. "localhost:10010" → 10010) so lncli targets the right port.
		port, err := portFromAddr(ns.Connection.GRPCUrl)
		if err != nil {
			return nil, fmt.Errorf("lnd %q: invalid grpc_url %q: %w", name, ns.Connection.GRPCUrl, err)
		}
		return &nodes.LNDNode{Name: name, ContainerName: ns.ContainerName, GRPCPort: port}, nil
	case "cln":
		return &nodes.CLNNode{Name: name, ContainerName: ns.ContainerName}, nil
	case "ldk":
		return &nodes.LDKNode{Name: name, ContainerName: ns.ContainerName}, nil
	default:
		return nil, fmt.Errorf("unknown node kind %q for node %q", ns.Kind, name)
	}
}

// portFromAddr parses the port from a host:port address string.
func portFromAddr(addr string) (int, error) {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(portStr)
}
