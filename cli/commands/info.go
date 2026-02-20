package commands

import (
	"fmt"

	"github.com/elnosh/lightnet/cli/state"
)

// RunInfo prints connection info for a network (and optionally a single node).
func RunInfo(networkName, nodeName string) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	if nodeName != "" {
		n, ok := net.Nodes[nodeName]
		if !ok {
			return fmt.Errorf("node %q not found in network %q", nodeName, networkName)
		}
		printNodeInfo(nodeName, n)
		return nil
	}

	printNetworkInfo(*net)
	return nil
}

func printNetworkInfo(net state.RunningNetwork) {
	fmt.Printf("Network: %s (%s)\n", net.Name, net.Status)
	fmt.Printf("Docker network: %s\n\n", net.DockerNetwork)

	for name, n := range net.Nodes {
		printNodeInfo(name, n)
		fmt.Println()
	}
}

func printNodeInfo(name string, n state.NodeState) {
	fmt.Printf("%s (%s)\n", name, n.Kind)

	switch n.Kind {
	case "bitcoind":
		fmt.Printf("  RPC:          %s\n", n.Connection.RPCURL)
		printP2P(n.Connection)

	case "lnd":
		fmt.Printf("  gRPC:         %s\n", n.Connection.GRPCUrl)
		fmt.Printf("  REST:         %s\n", n.Connection.RESTUrl)
		printP2P(n.Connection)
		fmt.Printf("  TLS cert:     %s\n", n.Connection.TLSCertPath)
		fmt.Printf("  Macaroon:     %s\n", n.Connection.MacaroonPath)
		fmt.Printf("\n  lncli shortcut:\n")
		fmt.Printf("    lncli --rpcserver=%s \\\n", n.Connection.GRPCUrl)
		fmt.Printf("          --tlscertpath=%s \\\n", n.Connection.TLSCertPath)
		fmt.Printf("          --macaroonpath=%s \\\n", n.Connection.MacaroonPath)
		fmt.Printf("          <command>\n")

	case "cln":
		if n.Connection.GRPCUrl != "" {
			fmt.Printf("  gRPC:         %s\n", n.Connection.GRPCUrl)
		}
		printP2P(n.Connection)
		if n.Connection.RPCSocketPath != "" {
			fmt.Printf("\n  lightning-cli shortcut:\n")
			fmt.Printf("    lightning-cli --network=regtest \\\n")
			fmt.Printf("                  --rpc-file=%s \\\n", n.Connection.RPCSocketPath)
			fmt.Printf("                  <command>\n")
		}

	case "ldk":
		if n.Connection.LDKRESTUrl != "" {
			fmt.Printf("  REST:         %s\n", n.Connection.LDKRESTUrl)
		}
		printP2P(n.Connection)

	default:
		if n.Connection.RPCURL != "" {
			fmt.Printf("  RPC:          %s\n", n.Connection.RPCURL)
		}
		if n.Connection.GRPCUrl != "" {
			fmt.Printf("  gRPC:         %s\n", n.Connection.GRPCUrl)
		}
		printP2P(n.Connection)
	}
}

func printP2P(c state.ConnectionInfo) {
	if c.P2PInternal != "" || c.P2PExternal != "" {
		fmt.Printf("  P2P (internal): %s\n", c.P2PInternal)
		fmt.Printf("  P2P (external): %s\n", c.P2PExternal)
	}
}
