package config

// NetworkConfig is the top-level YAML structure for a network definition.
type NetworkConfig struct {
	Name      string        `yaml:"name"`
	Bitcoind  []BitcoindConfig `yaml:"bitcoind"`
	LND       []LightningNodeConfig `yaml:"lnd"`
	CLN       []LightningNodeConfig `yaml:"cln"`
	LDKServer []LightningNodeConfig `yaml:"ldk_server"`
}

type BitcoindConfig struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

type LightningNodeConfig struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	ConnectsTo string `yaml:"connects_to"`
}
