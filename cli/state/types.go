package state

// GlobalState is the top-level structure written to ~/.lightnet/state.json.
// It is the shared contract between the Go CLI and the Rust scenarios runner.
type GlobalState struct {
	Version  int                       `json:"version"`
	Networks map[string]RunningNetwork `json:"networks"`
}

// RunningNetwork represents a started network and all its nodes.
type RunningNetwork struct {
	Name          string                 `json:"name"`
	Status        string                 `json:"status"` // "running" | "stopped"
	DockerNetwork string                 `json:"docker_network"`
	Nodes         map[string]NodeState   `json:"nodes"`
}

// NodeState holds runtime state and connection info for a single node.
type NodeState struct {
	Kind          string         `json:"kind"`           // "bitcoind" | "lnd" | "cln" | "ldk"
	ContainerName string         `json:"container_name"`
	Connection    ConnectionInfo `json:"connection"`
}

// ConnectionInfo holds the data needed to connect to a node.
// Fields are populated based on node kind.
type ConnectionInfo struct {
	// bitcoind
	RPCURL string `json:"rpc_url,omitempty"`

	// lnd / cln
	GRPCUrl      string `json:"grpc_url,omitempty"`
	RESTUrl      string `json:"rest_url,omitempty"`
	MacaroonPath string `json:"macaroon_path,omitempty"`
	TLSCertPath  string `json:"tls_cert_path,omitempty"`

	// cln
	RPCSocketPath string `json:"rpc_socket_path,omitempty"`

	// ldk
	LDKRESTUrl string `json:"ldk_rest_url,omitempty"`

	// P2P — all node types
	// Pubkey is the node's Lightning public key (hex). Only set for LN nodes.
	Pubkey string `json:"pubkey,omitempty"`
	// P2PInternal is the address reachable from other containers in the same
	// Docker network (container-name:port, resolved via Docker DNS).
	P2PInternal string `json:"p2p_internal,omitempty"`
	// P2PExternal is the host-mapped address reachable from the local machine.
	P2PExternal string `json:"p2p_external,omitempty"`
}
