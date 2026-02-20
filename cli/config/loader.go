package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultBitcoindRPCPort = 18443
	DefaultBitcoindP2PPort = 18444
	DefaultLNDGRPCPort     = 10009
	DefaultLNDRESTPort     = 8080
	DefaultLNDP2PPort      = 9735 // host-mapped port; inside container always 9735
	DefaultCLNGRPCPort     = 9736
	DefaultCLNP2PPort      = 19735 // host-mapped; inside container always 9735
	DefaultLDKRESTPort     = 3000
)

// LoadNetwork resolves and parses a network YAML file.
// It checks cwd first, then ~/.lightnet/networks/<name>.yaml.
func LoadNetwork(nameOrPath string) (*NetworkConfig, error) {
	path, err := resolveNetworkFile(nameOrPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading network file: %w", err)
	}

	var cfg NetworkConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing network file: %w", err)
	}

	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid network config: %w", err)
	}

	return &cfg, nil
}

func resolveNetworkFile(nameOrPath string) (string, error) {
	// Direct path check (e.g. "test.yaml" or absolute path)
	if _, err := os.Stat(nameOrPath); err == nil {
		return nameOrPath, nil
	}

	// Try appending .yaml
	withExt := nameOrPath + ".yaml"
	if _, err := os.Stat(withExt); err == nil {
		return withExt, nil
	}

	// Try ~/.lightnet/networks/<name>.yaml
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	lightnetPath := filepath.Join(home, ".lightnet", "networks", nameOrPath+".yaml")
	if _, err := os.Stat(lightnetPath); err == nil {
		return lightnetPath, nil
	}

	return "", fmt.Errorf("network file not found: %q (tried cwd and ~/.lightnet/networks/)", nameOrPath)
}

func applyDefaults(cfg *NetworkConfig) {
	for i := range cfg.Bitcoind {
		n := &cfg.Bitcoind[i]
		if n.RPCPort == 0 {
			n.RPCPort = DefaultBitcoindRPCPort + i
		}
		if n.P2PPort == 0 {
			n.P2PPort = DefaultBitcoindP2PPort + i
		}
	}

	firstBitcoind := ""
	if len(cfg.Bitcoind) > 0 {
		firstBitcoind = cfg.Bitcoind[0].Name
	}

	for i := range cfg.LND {
		n := &cfg.LND[i]
		if n.GRPCPort == 0 {
			n.GRPCPort = DefaultLNDGRPCPort + i
		}
		if n.RESTPort == 0 {
			n.RESTPort = DefaultLNDRESTPort + i
		}
		if n.P2PPort == 0 {
			n.P2PPort = DefaultLNDP2PPort + i
		}
		if n.ConnectsTo == "" {
			n.ConnectsTo = firstBitcoind
		}
	}

	for i := range cfg.CLN {
		n := &cfg.CLN[i]
		if n.GRPCPort == 0 {
			n.GRPCPort = DefaultCLNGRPCPort + i
		}
		if n.P2PPort == 0 {
			n.P2PPort = DefaultCLNP2PPort + i
		}
		if n.ConnectsTo == "" {
			n.ConnectsTo = firstBitcoind
		}
	}

	for i := range cfg.LDKServer {
		n := &cfg.LDKServer[i]
		if n.RESTPort == 0 {
			n.RESTPort = DefaultLDKRESTPort + i
		}
		if n.ConnectsTo == "" {
			n.ConnectsTo = firstBitcoind
		}
	}
}

func validate(cfg *NetworkConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("network name is required")
	}

	// Collect all node names and check uniqueness
	names := make(map[string]string) // name -> type
	addName := func(name, kind string) error {
		if name == "" {
			return fmt.Errorf("%s node has empty name", kind)
		}
		if prev, ok := names[name]; ok {
			return fmt.Errorf("duplicate node name %q (used by %s and %s)", name, prev, kind)
		}
		names[name] = kind
		return nil
	}

	bitcoindNames := make(map[string]bool)
	for _, n := range cfg.Bitcoind {
		if err := addName(n.Name, "bitcoind"); err != nil {
			return err
		}
		bitcoindNames[n.Name] = true
	}
	for _, n := range cfg.LND {
		if err := addName(n.Name, "lnd"); err != nil {
			return err
		}
		if n.ConnectsTo != "" && !bitcoindNames[n.ConnectsTo] {
			return fmt.Errorf("lnd %q: connects_to %q is not a known bitcoind node", n.Name, n.ConnectsTo)
		}
	}
	for _, n := range cfg.CLN {
		if err := addName(n.Name, "cln"); err != nil {
			return err
		}
		if n.ConnectsTo != "" && !bitcoindNames[n.ConnectsTo] {
			return fmt.Errorf("cln %q: connects_to %q is not a known bitcoind node", n.Name, n.ConnectsTo)
		}
	}
	for _, n := range cfg.LDKServer {
		if err := addName(n.Name, "ldk"); err != nil {
			return err
		}
		if n.ConnectsTo != "" && !bitcoindNames[n.ConnectsTo] {
			return fmt.Errorf("ldk %q: connects_to %q is not a known bitcoind node", n.Name, n.ConnectsTo)
		}
	}

	// Check host port collisions
	ports := make(map[int]string)
	checkPort := func(port int, desc string) error {
		if port == 0 {
			return nil
		}
		if prev, ok := ports[port]; ok {
			return fmt.Errorf("port %d used by both %s and %s", port, prev, desc)
		}
		ports[port] = desc
		return nil
	}

	for _, n := range cfg.Bitcoind {
		if err := checkPort(n.RPCPort, n.Name+"/rpc"); err != nil {
			return err
		}
		if err := checkPort(n.P2PPort, n.Name+"/p2p"); err != nil {
			return err
		}
	}
	for _, n := range cfg.LND {
		if err := checkPort(n.GRPCPort, n.Name+"/grpc"); err != nil {
			return err
		}
		if err := checkPort(n.RESTPort, n.Name+"/rest"); err != nil {
			return err
		}
		if err := checkPort(n.P2PPort, n.Name+"/p2p"); err != nil {
			return err
		}
	}
	for _, n := range cfg.CLN {
		if err := checkPort(n.GRPCPort, n.Name+"/grpc"); err != nil {
			return err
		}
		if err := checkPort(n.P2PPort, n.Name+"/p2p"); err != nil {
			return err
		}
	}
	for _, n := range cfg.LDKServer {
		if err := checkPort(n.RESTPort, n.Name+"/rest"); err != nil {
			return err
		}
	}

	return nil
}
