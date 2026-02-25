# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

`lightnet` is a CLI tool for spinning up declarative Lightning Network node networks using Docker. Networks are defined in YAML files. Docker images are built locally from Dockerfiles embedded in the binary (`cli/dockerfiles/`) — no remote image registry is used.

## Commands

```bash
# Build the CLI binary (from cli/ directory)
cd cli && go build -o ../bin/lightnet .

# Run tests (from cli/ directory)
cd cli && go test ./...

# Lint
cd cli && go vet ./...
```

The binary is output to `bin/lightnet` at the repo root.

## CLI Usage

```bash
lightnet start <network-yaml-or-name>           # Start a network
lightnet start <network-yaml-or-name> --rebuild # Force-rebuild images even if they exist locally
lightnet stop <network>                 # Stop and remove containers
lightnet info <network>                 # Show connection info for all nodes
lightnet list                           # List all known networks
lightnet fund <network> <address> <amount>      # Send BTC and mine 6 confirming blocks

# Execute a command on a node (main dispatch path in main.go)
lightnet <network> <node> [cmd...]
# e.g.: lightnet mynetwork alice listchannels
```

The `main.go` entry point routes any unrecognized first argument to `commands.RunNodeExec`, treating it as a network name. Known subcommands (`start`, `stop`, `info`, `list`, `fund`, `version`) go through Cobra.

## Architecture

```
cli/
├── main.go              # Entry point — routes node-exec vs. cobra commands
├── cmd/                 # Cobra command definitions (wires flags, calls commands/)
├── commands/            # Business logic for each command
│   ├── start.go         # Orchestrates Docker network + container lifecycle
│   ├── stop.go
│   ├── info.go
│   ├── nodeexec.go      # Dispatches to node.BuildCommand() then docker exec
│   └── list.go
├── config/
│   ├── network.go       # NetworkConfig struct (YAML schema)
│   └── loader.go        # YAML parsing, defaults, validation
├── docker/              # Thin wrappers around Docker SDK (client, network, containers, exec)
│   └── images.go        # LocalImageName, ImageExists, BuildImage (builds from embedded FS)
├── dockerfiles/         # Embedded Dockerfiles and support files (embed.go bundles these into binary)
│   ├── embed.go         # //go:embed all:bitcoind all:lnd all:clightning all:ldk-server
│   ├── bitcoind/        # Dockerfile, docker-entrypoint.sh, bashrc
│   ├── lnd/             # Dockerfile, docker-entrypoint.sh, bashrc
│   ├── clightning/      # Dockerfile, docker-entrypoint.sh, bashrc
│   └── ldk-server/      # Dockerfile, docker-entrypoint.sh, bashrc
├── nodes/               # Per-node-type config generation + readiness polling
│   ├── node.go          # Node interface: Kind() + BuildCommand(userArgs)
│   ├── bitcoind.go
│   ├── lnd.go
│   ├── cln.go
│   └── ldk.go
└── state/
    ├── types.go         # GlobalState / RunningNetwork / NodeState / ConnectionInfo
    └── manager.go       # Load/Save/AddNetwork/RemoveNetwork — persists to ~/.lightnet/state.json
```

### Key Design Points

**Network YAML → State JSON flow:** `config.LoadNetwork` parses the YAML, `commands/start.go` uses it to create Docker containers, then writes runtime state to `~/.lightnet/state.json` via `state.AddNetwork`. Subsequent commands (stop, info, node-exec) read only from state.json — they do not re-read the YAML.

**Node data directories:** Each node's config files and data are stored at `~/.lightnet/networks/<network>/<node>/data/` and bind-mounted into the container.

**Container naming:** `lightnet-<networkName>-<nodeName>` (see `docker.ContainerName`).

**Readiness polling:** Bitcoind nodes use `bitcoin-cli getblockchaininfo` polling. LND uses `lncli listchannels` (not `getinfo`, which succeeds too early). CLN uses a similar exec poll. Initial 101 blocks are mined on bitcoind startup so lightning nodes don't see `initialblockdownload=true`.

**LN nodes start concurrently:** All LND and CLN nodes for a network start in parallel goroutines after bitcoind is ready; results are collected via a channel.

**Node interface:** Each node type implements `BuildCommand(userArgs []string) []string` which translates bare user args (e.g. `["listchannels"]`) into the full `docker exec` command (e.g. `["lncli", "--network=regtest", ..., "listchannels"]`).

**Local image building:** On `start`, each node's image is built from the embedded Dockerfile if it doesn't already exist locally (or if `--rebuild` is passed). Images are tagged `lightnet-<nodeType>:<version>` (e.g. `lightnet-lnd:0.20.1-beta`). The Dockerfiles accept a version as a build arg (`BITCOIN_VERSION`, `LND_VERSION`, `CLN_VERSION`, `LDK_SERVER_VERSION`) and download the appropriate release binary at build time.

## Network YAML Schema

```yaml
name: mynetwork
bitcoind:
  - name: btc1
    version: "30.2"     # controls which release is downloaded in the Dockerfile build
lnd:
  - name: alice
    version: "0.20.1-beta"
    connects_to: btc1   # defaults to first bitcoind
cln:
  - name: carol
    version: "25.12"
    connects_to: btc1
ldk_server:
  - name: dave
    connects_to: btc1
```

Network files are resolved in order: exact path → `<name>.yaml` in cwd → `~/.lightnet/networks/<name>.yaml`.
