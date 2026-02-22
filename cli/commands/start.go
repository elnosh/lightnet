package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/elnosh/lightnet/cli/config"
	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/dockerfiles"
	"github.com/elnosh/lightnet/cli/nodes"
	"github.com/elnosh/lightnet/cli/state"
)

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

	// Step 2: Generate configs and start bitcoind nodes
	for _, btc := range cfg.Bitcoind {
		containerName := dockerpkg.ContainerName(cfg.Name, btc.Name)

		fmt.Printf("Configuring bitcoind node %s...\n", btc.Name)
		dataDir, err := nodes.GenerateBitcoindConfig(cfg.Name, btc.Name)
		if err != nil {
			return fmt.Errorf("bitcoind %s config: %w", btc.Name, err)
		}

		imageTag := dockerpkg.LocalImageName("bitcoind", btc.Version)
		exists, err := dockerpkg.ImageExists(ctx, c, imageTag)
		if err != nil {
			return err
		}
		if !exists || rebuild {
			fmt.Printf("Building image %s...\n", imageTag)
			if err := dockerpkg.BuildImage(ctx, c, dockerfiles.FS, "bitcoind", "BITCOIN_VERSION", btc.Version, imageTag); err != nil {
				return err
			}
		}

		opts := dockerpkg.CreateContainerOptions{
			Name:        containerName,
			Image:       imageTag,
			NetworkName: dockerNet,
			Ports: []dockerpkg.PortBinding{
				{HostPort: btc.RPCPort, ContainerPort: 18443},
				{HostPort: btc.P2PPort, ContainerPort: 18444},
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
				RPCURL:      fmt.Sprintf("http://lightnet:lightnet@127.0.0.1:%d", btc.RPCPort),
				P2PInternal: fmt.Sprintf("%s:18444", containerName),
				P2PExternal: fmt.Sprintf("127.0.0.1:%d", btc.P2PPort),
			},
		}
	}

	// Step 3: Start lightning nodes concurrently
	type startResult struct {
		name  string
		state state.NodeState
		err   error
	}
	results := make(chan startResult)
	var wg sync.WaitGroup

	// Find bitcoind config by name
	getBitcoind := func(name string) *config.BitcoindConfig {
		for i := range cfg.Bitcoind {
			if cfg.Bitcoind[i].Name == name {
				return &cfg.Bitcoind[i]
			}
		}
		return nil
	}

	// LND nodes
	for _, lndCfg := range cfg.LND {
		wg.Add(1)
		go func() {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, lndCfg.Name)

			btc := getBitcoind(lndCfg.ConnectsTo)
			if btc == nil {
				results <- startResult{name: lndCfg.Name, err: fmt.Errorf("lnd %s: bitcoind %q not found", lndCfg.Name, lndCfg.ConnectsTo)}
				return
			}
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, btc.Name)

			dataDir, err := nodes.GenerateLNDConfig(cfg.Name, lndCfg.Name, bitcoindContainer, btc.RPCPort, lndCfg.GRPCPort, lndCfg.RESTPort)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: fmt.Errorf("lnd %s config: %w", lndCfg.Name, err)}
				return
			}

			imageTag := dockerpkg.LocalImageName("lnd", lndCfg.Version)
			exists, err := dockerpkg.ImageExists(ctx, c, imageTag)
			if err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
				return
			}
			if !exists || rebuild {
				fmt.Printf("Building image %s...\n", imageTag)
				if err := dockerpkg.BuildImage(ctx, c, dockerfiles.FS, "lnd", "LND_VERSION", lndCfg.Version, imageTag); err != nil {
					results <- startResult{name: lndCfg.Name, err: err}
					return
				}
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Ports: []dockerpkg.PortBinding{
					{HostPort: lndCfg.GRPCPort, ContainerPort: lndCfg.GRPCPort},
					{HostPort: lndCfg.RESTPort, ContainerPort: lndCfg.RESTPort},
					{HostPort: lndCfg.P2PPort, ContainerPort: nodes.LNDP2PContainerPort},
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

			if err := nodes.WaitLNDReady(ctx, c, containerName, lndCfg.GRPCPort, 90*time.Second); err != nil {
				results <- startResult{name: lndCfg.Name, err: err}
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
						GRPCUrl:      fmt.Sprintf("localhost:%d", lndCfg.GRPCPort),
						RESTUrl:      fmt.Sprintf("https://localhost:%d", lndCfg.RESTPort),
						MacaroonPath: macaroonPath,
						TLSCertPath:  tlsCertPath,
						P2PInternal:  fmt.Sprintf("%s:%d", containerName, nodes.LNDP2PContainerPort),
						P2PExternal:  fmt.Sprintf("127.0.0.1:%d", lndCfg.P2PPort),
					},
				},
			}
		}()
	}

	// CLN nodes
	for _, clnCfg := range cfg.CLN {
		wg.Add(1)
		go func() {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, clnCfg.Name)

			btc := getBitcoind(clnCfg.ConnectsTo)
			if btc == nil {
				results <- startResult{name: clnCfg.Name, err: fmt.Errorf("cln %s: bitcoind %q not found", clnCfg.Name, clnCfg.ConnectsTo)}
				return
			}
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, btc.Name)

			dataDir, err := nodes.GenerateCLNConfig(cfg.Name, clnCfg.Name, bitcoindContainer, btc.RPCPort, clnCfg.GRPCPort)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: fmt.Errorf("cln %s config: %w", clnCfg.Name, err)}
				return
			}

			imageTag := dockerpkg.LocalImageName("clightning", clnCfg.Version)
			exists, err := dockerpkg.ImageExists(ctx, c, imageTag)
			if err != nil {
				results <- startResult{name: clnCfg.Name, err: err}
				return
			}
			if !exists || rebuild {
				fmt.Printf("Building image %s...\n", imageTag)
				if err := dockerpkg.BuildImage(ctx, c, dockerfiles.FS, "clightning", "CLN_VERSION", clnCfg.Version, imageTag); err != nil {
					results <- startResult{name: clnCfg.Name, err: err}
					return
				}
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Ports: []dockerpkg.PortBinding{
					{HostPort: clnCfg.GRPCPort, ContainerPort: clnCfg.GRPCPort},
					{HostPort: clnCfg.P2PPort, ContainerPort: nodes.CLNP2PContainerPort},
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

			rpcSocket := filepath.Join(home, ".lightnet", "networks", cfg.Name, clnCfg.Name, "data", "regtest", "lightning-rpc")

			results <- startResult{
				name: clnCfg.Name,
				state: state.NodeState{
					Kind:          "cln",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						GRPCUrl:       fmt.Sprintf("localhost:%d", clnCfg.GRPCPort),
						RPCSocketPath: rpcSocket,
						P2PInternal:   fmt.Sprintf("%s:%d", containerName, nodes.CLNP2PContainerPort),
						P2PExternal:   fmt.Sprintf("127.0.0.1:%d", clnCfg.P2PPort),
					},
				},
			}
		}()
	}

	// LDK server nodes
	for _, ldkCfg := range cfg.LDKServer {
		wg.Add(1)
		go func() {
			defer wg.Done()
			containerName := dockerpkg.ContainerName(cfg.Name, ldkCfg.Name)

			btc := getBitcoind(ldkCfg.ConnectsTo)
			if btc == nil {
				results <- startResult{name: ldkCfg.Name, err: fmt.Errorf("ldk %s: bitcoind %q not found", ldkCfg.Name, ldkCfg.ConnectsTo)}
				return
			}
			bitcoindContainer := dockerpkg.ContainerName(cfg.Name, btc.Name)

			dataDir, err := nodes.GenerateLDKConfig(cfg.Name, ldkCfg.Name, bitcoindContainer, btc.RPCPort, ldkCfg.P2PPort)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: fmt.Errorf("ldk %s config: %w", ldkCfg.Name, err)}
				return
			}

			imageTag := dockerpkg.LocalImageName("ldk", "latest")
			exists, err := dockerpkg.ImageExists(ctx, c, imageTag)
			if err != nil {
				results <- startResult{name: ldkCfg.Name, err: err}
				return
			}
			if !exists || rebuild {
				fmt.Printf("Building image %s...\n", imageTag)
				if err := dockerpkg.BuildImage(ctx, c, dockerfiles.FS, "ldk-server", "", "", imageTag); err != nil {
					results <- startResult{name: ldkCfg.Name, err: err}
					return
				}
			}

			opts := dockerpkg.CreateContainerOptions{
				Name:        containerName,
				Image:       imageTag,
				NetworkName: dockerNet,
				Cmd:         []string{"/data/ldk-config.toml"},
				Ports: []dockerpkg.PortBinding{
					{HostPort: ldkCfg.RESTPort, ContainerPort: nodes.LDKRESTContainerPort},
					{HostPort: ldkCfg.P2PPort, ContainerPort: nodes.LDKP2PContainerPort},
				},
				Mounts: []dockerpkg.VolumeMount{
					{HostPath: dataDir, ContainerPath: "/data"},
				},
				Env: []string{
					fmt.Sprintf("LDK_SERVER_BITCOIND_RPC_ADDRESS=%s:%d", bitcoindContainer, btc.RPCPort),
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

			results <- startResult{
				name: ldkCfg.Name,
				state: state.NodeState{
					Kind:          "ldk",
					ContainerName: containerName,
					Connection: state.ConnectionInfo{
						LDKRESTUrl:  fmt.Sprintf("https://localhost:%d", ldkCfg.RESTPort),
						P2PInternal: fmt.Sprintf("%s:%d", containerName, nodes.LDKP2PContainerPort),
						P2PExternal: fmt.Sprintf("127.0.0.1:%d", ldkCfg.P2PPort),
					},
				},
			}
		}()
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
