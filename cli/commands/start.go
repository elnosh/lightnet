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

	// Check if already running
	existing, _ := state.GetNetwork(cfg.Name)
	if existing != nil && existing.Status == "running" {
		return fmt.Errorf("network %q is already running", cfg.Name)
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	dockerNet := dockerpkg.NetworkName(cfg.Name)

	// Step 1: Create Docker bridge network
	fmt.Printf("Creating Docker network %s...\n", dockerNet)
	if err := dockerpkg.CreateNetwork(ctx, c, cfg.Name); err != nil {
		return err
	}

	networkState := state.RunningNetwork{
		Name:          cfg.Name,
		Status:        "running",
		DockerNetwork: dockerNet,
		Nodes:         make(map[string]state.NodeState),
	}

	home, _ := os.UserHomeDir()

	pa := newPortAllocator()
	// bitcoindRPCPorts maps containerName -> allocated RPC port so LN nodes can reference it.
	bitcoindRPCPorts := make(map[string]int)

	// Step 2: Generate configs and start bitcoind nodes
	for _, btc := range cfg.Bitcoind {
		containerName := dockerpkg.ContainerName(cfg.Name, btc.Name)

		rpcPort, err := pa.next(baseBitcoindRPC)
		if err != nil {
			return fmt.Errorf("bitcoind %s: %w", btc.Name, err)
		}
		p2pPort, err := pa.next(baseBitcoindP2P)
		if err != nil {
			return fmt.Errorf("bitcoind %s: %w", btc.Name, err)
		}

		bitcoindRPCPorts[containerName] = rpcPort

		fmt.Printf("Configuring bitcoind node %s...\n", btc.Name)
		dataDir, err := nodes.GenerateBitcoindConfig(cfg.Name, btc.Name)
		if err != nil {
			return fmt.Errorf("bitcoind %s config: %w", btc.Name, err)
		}

		imageTag, err := ensureImage(ctx, c, "bitcoind", btc.Version, "BITCOIN_VERSION", rebuild)
		if err != nil {
			return err
		}

		opts := dockerpkg.CreateContainerOptions{
			Name:        containerName,
			Image:       imageTag,
			NetworkName: dockerNet,
			Ports: []dockerpkg.PortBinding{
				{HostPort: rpcPort, ContainerPort: 18443},
				{HostPort: p2pPort, ContainerPort: 18444},
			},
			Mounts: []dockerpkg.VolumeMount{
				{HostPath: dataDir, ContainerPath: "/home/bitcoin/.bitcoin"},
			},
		}

		fmt.Printf("Starting bitcoind node %s...\n", btc.Name)
		id, err := dockerpkg.CreateContainer(ctx, c, opts)
		if err != nil {
			return err
		}
		if err := dockerpkg.StartContainer(ctx, c, id); err != nil {
			return err
		}

		fmt.Printf("Waiting for bitcoind %s to be ready...\n", btc.Name)
		if err := nodes.WaitUntilReady(ctx, c, containerName, 60*time.Second); err != nil {
			return err
		}

		// Mine initial blocks so bitcoind exits IBD mode.
		// The regtest genesis block is from 2011, so without mining, bitcoind
		// reports initialblockdownload=true forever and LND/CLN won't start.
		fmt.Printf("Mining initial blocks on %s...\n", btc.Name)
		if err := nodes.MineInitialBlocks(ctx, c, containerName, 101); err != nil {
			return fmt.Errorf("mining initial blocks on %s: %w", btc.Name, err)
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
	// Ports are allocated serially here before goroutines launch; the
	// bitcoindRPCPorts map is only read (never written) inside goroutines.
	type startResult struct {
		name  string
		state state.NodeState
		err   error
	}
	results := make(chan startResult)
	var wg sync.WaitGroup

	// LND nodes
	for _, lndCfg := range cfg.LND {
		grpcPort, err := pa.next(baseLNDGRPC)
		if err != nil {
			return fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}
		restPort, err := pa.next(baseLNDREST)
		if err != nil {
			return fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}
		p2pPort, err := pa.next(baseLNDP2P)
		if err != nil {
			return fmt.Errorf("lnd %s: %w", lndCfg.Name, err)
		}

		wg.Add(1)
		go func(lndCfg config.LightningNodeConfig, grpcPort, restPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, lndCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, lndCfg.ConnectsTo)
			btcRPCPort := bitcoindRPCPorts[bitcoindContainer]

			dataDir, err := nodes.GenerateLNDConfig(cfg.Name, lndCfg.Name, bitcoindContainer, btcRPCPort, grpcPort, restPort)
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
			return fmt.Errorf("cln %s: %w", clnCfg.Name, err)
		}
		p2pPort, err := pa.next(baseCLNP2P)
		if err != nil {
			return fmt.Errorf("cln %s: %w", clnCfg.Name, err)
		}

		wg.Add(1)
		go func(clnCfg config.LightningNodeConfig, grpcPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, clnCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, clnCfg.ConnectsTo)
			btcRPCPort := bitcoindRPCPorts[bitcoindContainer]

			dataDir, err := nodes.GenerateCLNConfig(cfg.Name, clnCfg.Name, bitcoindContainer, btcRPCPort, grpcPort)
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

			rpcSocket := filepath.Join(home, ".lightnet", "networks", cfg.Name, clnCfg.Name, "data", "regtest", "lightning-rpc")

			results <- startResult{
				name: clnCfg.Name,
				state: state.NodeState{
					Kind:          "cln",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						GRPCUrl:       fmt.Sprintf("localhost:%d", grpcPort),
						RPCSocketPath: rpcSocket,
						Pubkey:        pubkey,
						P2PInternal:   fmt.Sprintf("%s:%d", containerName, nodes.CLNP2PContainerPort),
						P2PExternal:   fmt.Sprintf("127.0.0.1:%d", p2pPort),
					},
				},
			}
		}(clnCfg, grpcPort, p2pPort)
	}

	// LDK server nodes
	for _, ldkCfg := range cfg.LDKServer {
		restPort, err := pa.next(baseLDKREST)
		if err != nil {
			return fmt.Errorf("ldk %s: %w", ldkCfg.Name, err)
		}
		p2pPort, err := pa.next(baseLDKP2P)
		if err != nil {
			return fmt.Errorf("ldk %s: %w", ldkCfg.Name, err)
		}

		wg.Add(1)
		go func(ldkCfg config.LightningNodeConfig, restPort, p2pPort int) {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, ldkCfg.Name)
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, ldkCfg.ConnectsTo)
			btcRPCPort := bitcoindRPCPorts[bitcoindContainer]

			dataDir, err := nodes.GenerateLDKConfig(cfg.Name, ldkCfg.Name, bitcoindContainer, btcRPCPort)
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
					fmt.Sprintf("LDK_SERVER_BITCOIND_RPC_ADDRESS=%s:%d", bitcoindContainer, btcRPCPort),
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

	// Collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		if r.err != nil {
			return fmt.Errorf("starting node %s: %w", r.name, r.err)
		}
		fmt.Printf("Node %s is ready.\n", r.name)
		networkState.Nodes[r.name] = r.state
	}

	// Step 4: Write state
	if err := state.AddNetwork(networkState); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	// Step 5: Print summary
	fmt.Println()
	printNetworkInfo(networkState)

	return nil
}
