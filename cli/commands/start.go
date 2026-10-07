package commands

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elnosh/lightnet/cli/config"
	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/dockerfiles"
	"github.com/elnosh/lightnet/cli/nodes"
	"github.com/elnosh/lightnet/cli/state"
	"github.com/moby/moby/client"
)

const (
	baseBitcoindRPC = 18443
	baseBitcoindP2P = 18444
	baseLNDGRPC     = 10009
	baseLNDREST     = 8080
	baseLNDP2P      = 9735
	baseCLNGRPC     = 9736
	baseCLNP2P      = 19735
	baseLDKREST     = 3000
	baseLDKP2P      = 29735
	baseEclairAPI   = 8280
	baseEclairP2P   = 39735
)

type portAllocator struct {
	allocated map[int]bool
}

func newPortAllocator() *portAllocator {
	return &portAllocator{allocated: make(map[int]bool)}
}

// next returns the lowest free port >= base that is not already allocated
// in this run and not currently bound on 127.0.0.1.
func (a *portAllocator) next(base int) (int, error) {
	for port := base; port < base+100; port++ {
		if a.allocated[port] {
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue // port in use on host
		}
		ln.Close()
		a.allocated[port] = true
		return port, nil
	}
	return 0, fmt.Errorf("no free port found near %d", base)
}

// ensureImage returns the local image tag for nodeType:version, building it
// from the embedded Dockerfiles if it doesn't exist or rebuild is true.
func ensureImage(ctx context.Context, c *client.Client, nodeType, version, buildArgKey string, rebuild bool) (string, error) {
	imageTag := dockerpkg.LocalImageName(nodeType, version)
	exists, err := dockerpkg.ImageExists(ctx, c, imageTag)
	if err != nil {
		return "", err
	}
	if !exists || rebuild {
		fmt.Printf("Building image %s...\n", imageTag)
		if err := dockerpkg.BuildImage(ctx, c, dockerfiles.FS, nodeType, buildArgKey, version, imageTag); err != nil {
			return "", err
		}
	}
	return imageTag, nil
}

// RunStart starts a network defined by nameOrPath (a YAML file or network name).
func RunStart(nameOrPath string, rebuild bool) error {
	cfg, err := config.LoadNetwork(nameOrPath)
	if err != nil {
		return err
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	// Reconcile any prior state with reality. If state.json says the network is
	// running but Docker disagrees (e.g. host was shut down without `lightnet
	// stop`), clear out the stale containers and docker network so we can start
	// fresh. Data directories are preserved, so node identities persist.
	existing, _ := state.GetNetwork(cfg.Name)
	if existing != nil && existing.Status == "running" {
		stale, err := isStaleNetwork(ctx, c, existing)
		if err != nil {
			return err
		}
		if !stale {
			return fmt.Errorf("network %q is already running", cfg.Name)
		}
		fmt.Printf("Network %q has stale state (containers not running); cleaning up before restart...\n", cfg.Name)
		var names []string
		for _, n := range existing.Nodes {
			names = append(names, n.ContainerName)
		}
		removeNetworkResources(ctx, c, cfg.Name, names)
	}

	// A start that failed or was interrupted never reaches state.json, but its
	// containers and docker network still hold the names we are about to use.
	removeNetworkResources(ctx, c, cfg.Name, containerNames(cfg))

	networkState, err := startNodes(ctx, c, cfg, rebuild)
	if err != nil {
		fmt.Printf("Stopping containers; they are kept for inspection (docker logs <container>) and removed on the next start.\n")
		for _, name := range containerNames(cfg) {
			if err := dockerpkg.StopContainer(ctx, c, name); err != nil {
				fmt.Printf("  warning: %v\n", err)
			}
		}
		return err
	}

	if err := state.AddNetwork(networkState); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	fmt.Println()
	printNetworkInfo(networkState)

	return nil
}

// startNodes cancels the remaining nodes on the first failure and waits for
// them before returning, so no container is created after the caller starts
// cleaning up.
func startNodes(ctx context.Context, c *client.Client, cfg *config.NetworkConfig, rebuild bool) (state.RunningNetwork, error) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	dockerNet := dockerpkg.NetworkName(cfg.Name)

	// Step 1: Create Docker bridge network
	fmt.Printf("Creating Docker network %s...\n", dockerNet)
	if err := dockerpkg.CreateNetwork(ctx, c, cfg.Name); err != nil {
		return state.RunningNetwork{}, err
	}

	networkState := state.RunningNetwork{
		Name:          cfg.Name,
		Status:        "running",
		DockerNetwork: dockerNet,
		Nodes:         make(map[string]state.NodeState),
	}

	home, _ := os.UserHomeDir()

	pa := newPortAllocator()

	// Step 2: Generate configs and start bitcoind nodes
	for _, btc := range cfg.Bitcoind {
		containerName := dockerpkg.ContainerName(cfg.Name, btc.Name)

		rpcPort, err := pa.next(baseBitcoindRPC)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("bitcoind %s: %w", btc.Name, err)
		}
		p2pPort, err := pa.next(baseBitcoindP2P)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("bitcoind %s: %w", btc.Name, err)
		}

		fmt.Printf("Configuring bitcoind node %s...\n", btc.Name)
		dataDir, err := nodes.GenerateBitcoindConfig(cfg.Name, btc.Name)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("bitcoind %s config: %w", btc.Name, err)
		}

		imageTag, err := ensureImage(ctx, c, "bitcoind", btc.Version, "BITCOIN_VERSION", rebuild)
		if err != nil {
			return state.RunningNetwork{}, err
		}

		opts := dockerpkg.CreateContainerOptions{
			Name:        containerName,
			Image:       imageTag,
			NetworkName: dockerNet,
			Ports: []dockerpkg.PortBinding{
				{HostPort: rpcPort, ContainerPort: nodes.BitcoindRPCContainerPort},
				{HostPort: p2pPort, ContainerPort: 18444},
			},
			Mounts: []dockerpkg.VolumeMount{
				{HostPath: dataDir, ContainerPath: "/home/bitcoin/.bitcoin"},
			},
		}

		fmt.Printf("Starting bitcoind node %s...\n", btc.Name)
		id, err := dockerpkg.CreateContainer(ctx, c, opts)
		if err != nil {
			return state.RunningNetwork{}, err
		}
		if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
			return state.RunningNetwork{}, err
		}

		fmt.Printf("Waiting for bitcoind %s to be ready...\n", btc.Name)
		if err := nodes.WaitUntilReady(ctx, c, containerName, 60*time.Second); err != nil {
			return state.RunningNetwork{}, err
		}

		// Mine initial blocks so bitcoind exits IBD mode.
		// The regtest genesis block is from 2011, so without mining, bitcoind
		// reports initialblockdownload=true forever and LND/CLN won't start.
		fmt.Printf("Mining initial blocks on %s...\n", btc.Name)
		if err := nodes.MineInitialBlocks(ctx, c, containerName, 101); err != nil {
			return state.RunningNetwork{}, fmt.Errorf("mining initial blocks on %s: %w", btc.Name, err)
		}

		networkState.Nodes[btc.Name] = state.NodeState{
			Kind:          "bitcoind",
			ContainerName: containerName,
			Connection: state.ConnectionInfo{
				RPCURL:      fmt.Sprintf("http://lightnet:lightnet@127.0.0.1:%d", rpcPort),
				P2PInternal: fmt.Sprintf("%s:18444", containerName),
				P2PExternal: fmt.Sprintf("127.0.0.1:%d", p2pPort),
			},
		}
	}

	// Step 3: Start lightning nodes concurrently.
	// Ports are allocated serially here before goroutines launch.
	type startResult struct {
		name  string
		state state.NodeState
		err   error
	}
	// Buffered so goroutines never block on send once collection has stopped.
	results := make(chan startResult, len(cfg.LND)+len(cfg.CLN)+len(cfg.LDKServer)+len(cfg.Eclair))

	// LND nodes
	for _, lndCfg := range cfg.LND {
		grpcPort, err := pa.next(baseLNDGRPC)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}
		restPort, err := pa.next(baseLNDREST)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}
		p2pPort, err := pa.next(baseLNDP2P)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}

		wg.Add(1)
		go func(lndCfg config.LightningNodeConfig, grpcPort, restPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, lndCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, lndCfg.ConnectsTo)
			dataDir, err := nodes.GenerateLNDConfig(cfg.Name, lndCfg.Name, bitcoindContainer, nodes.BitcoindRPCContainerPort, grpcPort, restPort)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: fmt.Errorf("lnd %s config: %w", lndCfg.Name, err)}
				return
			}

			imageTag, err := ensureImage(ctx, c, "lnd", lndCfg.Version, "LND_VERSION", rebuild)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
				return
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Ports: []dockerpkg.PortBinding{
					{HostPort: grpcPort, ContainerPort: grpcPort},
					{HostPort: restPort, ContainerPort: restPort},
					{HostPort: p2pPort, ContainerPort: nodes.LNDP2PContainerPort},
				},
				Mounts: []dockerpkg.VolumeMount{
					{HostPath: dataDir, ContainerPath: "/home/lnd/.lnd"},
				},
			}

			id, err := dockerpkg.CreateContainer(ctx, c, opts)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
				return
			}
			if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
				return
			}

			if err := nodes.WaitLNDReady(ctx, c, containerName, grpcPort, 90*time.Second); err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
				return
			}

			pubkey, err := nodes.FetchLNDPubkey(ctx, c, containerName, grpcPort)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: fmt.Errorf("lnd %s pubkey: %w", lndCfg.Name, err)}
				return
			}

			macaroonPath := filepath.Join(home, ".lightnet", "networks", cfg.Name, lndCfg.Name, "data", "data", "chain", "bitcoin", "regtest", "admin.macaroon")
			tlsCertPath := filepath.Join(home, ".lightnet", "networks", cfg.Name, lndCfg.Name, "data", "tls.cert")

			results <- startResult{
				name: lndCfg.Name,
				state: state.NodeState{
					Kind:          "lnd",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						GRPCUrl:      fmt.Sprintf("localhost:%d", grpcPort),
						RESTUrl:      fmt.Sprintf("https://localhost:%d", restPort),
						MacaroonPath: macaroonPath,
						TLSCertPath:  tlsCertPath,
						Pubkey:       pubkey,
						P2PInternal:  fmt.Sprintf("%s:%d", containerName, nodes.LNDP2PContainerPort),
						P2PExternal:  fmt.Sprintf("127.0.0.1:%d", p2pPort),
					},
				},
			}
		}(lndCfg, grpcPort, restPort, p2pPort)
	}

	// CLN nodes
	for _, clnCfg := range cfg.CLN {
		grpcPort, err := pa.next(baseCLNGRPC)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("cln %s: %w", clnCfg.Name, err)
		}
		p2pPort, err := pa.next(baseCLNP2P)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("cln %s: %w", clnCfg.Name, err)
		}

		wg.Add(1)
		go func(clnCfg config.LightningNodeConfig, grpcPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, clnCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, clnCfg.ConnectsTo)
			dataDir, err := nodes.GenerateCLNConfig(cfg.Name, clnCfg.Name, bitcoindContainer, nodes.BitcoindRPCContainerPort, grpcPort)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: fmt.Errorf("cln %s config: %w", clnCfg.Name, err)}
				return
			}

			imageTag, err := ensureImage(ctx, c, "clightning", clnCfg.Version, "CLN_VERSION", rebuild)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: err}
				return
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Ports: []dockerpkg.PortBinding{
					{HostPort: grpcPort, ContainerPort: grpcPort},
					{HostPort: p2pPort, ContainerPort: nodes.CLNP2PContainerPort},
				},
				Mounts: []dockerpkg.VolumeMount{
					{HostPath: dataDir, ContainerPath: "/home/clightning/.lightning"},
				},
			}

			id, err := dockerpkg.CreateContainer(ctx, c, opts)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: err}
				return
			}
			if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
				results <- startResult{name: clnCfg.Name, err: err}
				return
			}

			if err := nodes.WaitCLNReady(ctx, c, containerName, 90*time.Second); err != nil {
				results <- startResult{name: clnCfg.Name, err: err}
				return
			}

			pubkey, err := nodes.FetchCLNPubkey(ctx, c, containerName)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: fmt.Errorf("cln %s pubkey: %w", clnCfg.Name, err)}
				return
			}

			// lightningd runs from the network subdirectory, so the grpc plugin
			// writes its certificates there.
			netDir := filepath.Join(home, ".lightnet", "networks", cfg.Name, clnCfg.Name, "data", "regtest")
			rpcSocket := filepath.Join(netDir, "lightning-rpc")

			results <- startResult{
				name: clnCfg.Name,
				state: state.NodeState{
					Kind:          "cln",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						GRPCUrl:        fmt.Sprintf("localhost:%d", grpcPort),
						TLSCertPath:    filepath.Join(netDir, "ca.pem"),
						ClientCertPath: filepath.Join(netDir, "client.pem"),
						ClientKeyPath:  filepath.Join(netDir, "client-key.pem"),
						RPCSocketPath:  rpcSocket,
						Pubkey:         pubkey,
						P2PInternal:    fmt.Sprintf("%s:%d", containerName, nodes.CLNP2PContainerPort),
						P2PExternal:    fmt.Sprintf("127.0.0.1:%d", p2pPort),
					},
				},
			}
		}(clnCfg, grpcPort, p2pPort)
	}

	// LDK server nodes
	for _, ldkCfg := range cfg.LDKServer {
		restPort, err := pa.next(baseLDKREST)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("ldk %s: %w", ldkCfg.Name, err)
		}
		p2pPort, err := pa.next(baseLDKP2P)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("ldk %s: %w", ldkCfg.Name, err)
		}

		wg.Add(1)
		go func(ldkCfg config.LightningNodeConfig, restPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, ldkCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, ldkCfg.ConnectsTo)
			dataDir, err := nodes.GenerateLDKConfig(cfg.Name, ldkCfg.Name, bitcoindContainer, nodes.BitcoindRPCContainerPort)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: fmt.Errorf("ldk %s config: %w", ldkCfg.Name, err)}
				return
			}

			imageTag, err := ensureImage(ctx, c, "ldk-server", "latest", "", rebuild)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: err}
				return
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Cmd:         []string{"/data/ldk-config.toml"},
				Ports: []dockerpkg.PortBinding{
					{HostPort: restPort, ContainerPort: nodes.LDKRESTContainerPort},
					{HostPort: p2pPort, ContainerPort: nodes.LDKP2PContainerPort},
				},
				Mounts: []dockerpkg.VolumeMount{
					{HostPath: dataDir, ContainerPath: "/data"},
				},
				Env: []string{
					fmt.Sprintf("LDK_SERVER_BITCOIND_RPC_ADDRESS=%s:%d", bitcoindContainer, nodes.BitcoindRPCContainerPort),
					"LDK_SERVER_BITCOIND_RPC_USER=lightnet",
					"LDK_SERVER_BITCOIND_RPC_PASSWORD=lightnet",
				},
			}

			id, err := dockerpkg.CreateContainer(ctx, c, opts)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: err}
				return
			}
			if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
				results <- startResult{name: ldkCfg.Name, err: err}
				return
			}

			if err := nodes.WaitLDKReady(ctx, c, containerName, 90*time.Second); err != nil {
				results <- startResult{name: ldkCfg.Name, err: err}
				return
			}

			pubkey, err := nodes.FetchLDKPubkey(ctx, c, containerName)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: fmt.Errorf("ldk %s pubkey: %w", ldkCfg.Name, err)}
				return
			}

			results <- startResult{
				name: ldkCfg.Name,
				state: state.NodeState{
					Kind:          "ldk",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						LDKRESTUrl:  fmt.Sprintf("https://localhost:%d", restPort),
						Pubkey:      pubkey,
						P2PInternal: fmt.Sprintf("%s:%d", containerName, nodes.LDKP2PContainerPort),
						P2PExternal: fmt.Sprintf("127.0.0.1:%d", p2pPort),
					},
				},
			}
		}(ldkCfg, restPort, p2pPort)
	}

	// Eclair nodes
	for _, eclairCfg := range cfg.Eclair {
		apiPort, err := pa.next(baseEclairAPI)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("eclair %s: %w", eclairCfg.Name, err)
		}
		p2pPort, err := pa.next(baseEclairP2P)
		if err != nil {
			return state.RunningNetwork{}, fmt.Errorf("eclair %s: %w", eclairCfg.Name, err)
		}

		wg.Add(1)
		go func(eclairCfg config.LightningNodeConfig, apiPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, eclairCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, eclairCfg.ConnectsTo)

			dataDir, err := nodes.GenerateEclairConfig(cfg.Name, eclairCfg.Name, bitcoindContainer, nodes.BitcoindRPCContainerPort)
			if err != nil {
				results <- startResult{name: eclairCfg.Name, err: fmt.Errorf("eclair %s config: %w", eclairCfg.Name, err)}
				return
			}

			nodes.EnsureWallet(ctx, c, bitcoindContainer, nodes.EclairWalletName(eclairCfg.Name))

			imageTag, err := ensureImage(ctx, c, "eclair", eclairCfg.Version, "ECLAIR_VERSION", rebuild)
			if err != nil {
				results <- startResult{name: eclairCfg.Name, err: err}
				return
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Ports: []dockerpkg.PortBinding{
					{HostPort: apiPort, ContainerPort: nodes.EclairAPIContainerPort},
					{HostPort: p2pPort, ContainerPort: nodes.EclairP2PContainerPort},
				},
				Mounts: []dockerpkg.VolumeMount{
					{HostPath: dataDir, ContainerPath: "/home/eclair/.eclair"},
				},
			}

			id, err := dockerpkg.CreateContainer(ctx, c, opts)
			if err != nil {
				results <- startResult{name: eclairCfg.Name, err: err}
				return
			}
			if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
				results <- startResult{name: eclairCfg.Name, err: err}
				return
			}

			// JVM startup plus bitcoind sync is slower than the other implementations.
			if err := nodes.WaitEclairReady(ctx, c, containerName, 180*time.Second); err != nil {
				results <- startResult{name: eclairCfg.Name, err: err}
				return
			}

			pubkey, err := nodes.FetchEclairPubkey(ctx, c, containerName)
			if err != nil {
				results <- startResult{name: eclairCfg.Name, err: fmt.Errorf("eclair %s pubkey: %w", eclairCfg.Name, err)}
				return
			}

			results <- startResult{
				name: eclairCfg.Name,
				state: state.NodeState{
					Kind:          "eclair",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						RESTUrl:     fmt.Sprintf("http://localhost:%d", apiPort),
						Pubkey:      pubkey,
						P2PInternal: fmt.Sprintf("%s:%d", containerName, nodes.EclairP2PContainerPort),
						P2PExternal: fmt.Sprintf("127.0.0.1:%d", p2pPort),
					},
				},
			}
		}(eclairCfg, apiPort, p2pPort)
	}

	// Collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	var firstErr error
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("starting node %s: %w", r.name, r.err)
				cancel()
			}
			continue
		}
		fmt.Printf("Node %s is ready.\n", r.name)
		networkState.Nodes[r.name] = r.state
	}
	if firstErr != nil {
		return state.RunningNetwork{}, firstErr
	}

	return networkState, nil
}

func containerNames(cfg *config.NetworkConfig) []string {
	var names []string
	for _, n := range cfg.Bitcoind {
		names = append(names, dockerpkg.ContainerName(cfg.Name, n.Name))
	}
	for _, group := range [][]config.LightningNodeConfig{cfg.LND, cfg.CLN, cfg.LDKServer, cfg.Eclair} {
		for _, n := range group {
			names = append(names, dockerpkg.ContainerName(cfg.Name, n.Name))
		}
	}
	return names
}

// isStaleNetwork returns true if state.json claims the network is running but
// any of its containers is not actually running according to Docker.
func isStaleNetwork(ctx context.Context, c *client.Client, net *state.RunningNetwork) (bool, error) {
	for _, n := range net.Nodes {
		running, err := dockerpkg.ContainerRunning(ctx, c, n.ContainerName)
		if err != nil {
			return false, err
		}
		if !running {
			return true, nil
		}
	}
	return false, nil
}

// removeNetworkResources is best-effort: errors are logged but not fatal.
// Data directories under ~/.lightnet/networks/<name>/ are NOT touched, so node
// identities and chain state persist.
func removeNetworkResources(ctx context.Context, c *client.Client, networkName string, containers []string) {
	for _, name := range containers {
		// RemoveContainer uses Force=true which stops a running container
		// before removing it, so a separate Stop call isn't needed.
		if err := dockerpkg.RemoveContainer(ctx, c, name); err != nil {
			fmt.Printf("  warning: removing %s: %v\n", name, err)
		}
	}
	if err := dockerpkg.RemoveNetwork(ctx, c, networkName); err != nil {
		fmt.Printf("  warning: removing docker network: %v\n", err)
	}
}
