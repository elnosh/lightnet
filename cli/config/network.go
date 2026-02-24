package config

// NetworkConfig is the top-level YAML structure for a network definition.
type NetworkConfig struct {
	Name      string        `yaml:"name"`
	Bitcoind  []BitcoindConfig `yaml:"bitcoind"`
	LND       []LNDConfig      `yaml:"lnd"`
	CLN       []CLNConfig      `yaml:"cln"`
	LDKServer []LDKConfig      `yaml:"ldk_server"`
}

type BitcoindConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type LNDConfig struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	ConnectsTo string `yaml:"connects_to"`
}

type CLNConfig struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	ConnectsTo string `yaml:"connects_to"`
}

type LDKConfig struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	ConnectsTo string `yaml:"connects_to"`
}
