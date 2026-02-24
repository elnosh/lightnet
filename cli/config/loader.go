package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
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
	firstBitcoind := ""
	if len(cfg.Bitcoind) > 0 {
		firstBitcoind = cfg.Bitcoind[0].Name
	}

	for i := range cfg.LND {
		if cfg.LND[i].ConnectsTo == "" {
			cfg.LND[i].ConnectsTo = firstBitcoind
		}
	}

	for i := range cfg.CLN {
		if cfg.CLN[i].ConnectsTo == "" {
			cfg.CLN[i].ConnectsTo = firstBitcoind
		}
	}

	for i := range cfg.LDKServer {
		if cfg.LDKServer[i].ConnectsTo == "" {
			cfg.LDKServer[i].ConnectsTo = firstBitcoind
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

	return nil
}
