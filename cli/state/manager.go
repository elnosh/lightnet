package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const stateVersion = 1

func statePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lightnet", "state.json"), nil
}

// Load reads the current state from disk. Returns an empty GlobalState if the
// file does not exist yet.
func Load() (*GlobalState, error) {
	path, err := statePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &GlobalState{Version: stateVersion, Networks: make(map[string]RunningNetwork)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var gs GlobalState
	if err := json.Unmarshal(data, &gs); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	if gs.Networks == nil {
		gs.Networks = make(map[string]RunningNetwork)
	}
	return &gs, nil
}

// Save writes the state atomically to ~/.lightnet/state.json.
func Save(gs *GlobalState) error {
	path, err := statePath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing temp state: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming state file: %w", err)
	}

	return nil
}

// AddNetwork adds or replaces a network entry and saves.
func AddNetwork(net RunningNetwork) error {
	gs, err := Load()
	if err != nil {
		return err
	}
	gs.Networks[net.Name] = net
	return Save(gs)
}

// RemoveNetwork removes a network entry and saves.
func RemoveNetwork(name string) error {
	gs, err := Load()
	if err != nil {
		return err
	}
	delete(gs.Networks, name)
	return Save(gs)
}

// GetNetwork returns a network by name.
func GetNetwork(name string) (*RunningNetwork, error) {
	gs, err := Load()
	if err != nil {
		return nil, err
	}
	net, ok := gs.Networks[name]
	if !ok {
		return nil, fmt.Errorf("network %q not found in state (is it running?)", name)
	}
	return &net, nil
}

// UpdateNetworkStatus updates just the status field for a network.
func UpdateNetworkStatus(name, status string) error {
	gs, err := Load()
	if err != nil {
		return err
	}
	net, ok := gs.Networks[name]
	if !ok {
		return fmt.Errorf("network %q not found", name)
	}
	net.Status = status
	gs.Networks[name] = net
	return Save(gs)
}
