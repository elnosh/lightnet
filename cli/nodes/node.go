package nodes

// Node is the interface all node types implement.
type Node interface {
	// Kind returns the node type string ("bitcoind", "lnd", "cln", "ldk").
	Kind() string

	// BuildCommand translates user-facing args into the full command to run
	// inside the container (e.g. ["listchannels"] → ["lncli", "--network=regtest", "listchannels"]).
	BuildCommand(userArgs []string) []string
}
