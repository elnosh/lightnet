## Lightnet

Spin up declarative Lightning networks for testing using Docker. Networks are defined in YAML files.

This is like [Polar](https://github.com/jamaljsr/polar) but CLI native.

## Install

```bash
git clone https://github.com/elnosh/lightnet
cd lightnet/cli && go build -o ../bin/lightnet .
```

## Network file

```yaml
# mynetwork.yaml
name: mynetwork
bitcoind:
  - name: btc1
    version: "30.2"
lnd:
  - name: alice
    version: "0.20.1-beta"
    connects_to: btc1
  - name: bob
    version: "0.20.1-beta"
    connects_to: btc1
```

## Usage

```bash
# Start a network
lightnet start mynetwork.yaml

# Show connection info for all nodes
lightnet info mynetwork

# List all known networks
lightnet list

# Stop and remove containers in the network
lightnet stop mynetwork --remove

# Fund an address (sends BTC and mines 6 confirming blocks)
lightnet fund mynetwork <address> <amount>
```

## Node commands

Run commands directly on a node:

```bash
lightnet <network> <node> [cmd...]

# Examples
lightnet mynetwork alice getinfo
lightnet mynetwork alice listchannels
lightnet mynetwork alice openchannel --node_key=<pubkey> --local_amt=100000
lightnet mynetwork btc1 getblockchaininfo
```
