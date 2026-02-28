package nodes

import (
	"context"
	"fmt"
	"time"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/moby/moby/client"
)

// Node is the interface all node types implement.
type Node interface {
	// Kind returns the node type string ("bitcoind", "lnd", "cln", "ldk").
	Kind() string

	// BuildCommand translates user-facing args into the full command to run
	// inside the container (e.g. ["listchannels"] → ["lncli", "--network=regtest", "listchannels"]).
	BuildCommand(userArgs []string) []string
}

// pollUntilReady executes cmd inside containerName every second until it
// succeeds or timeout elapses. label is used in the error message ("bitcoind",
// "lnd", etc.).
func pollUntilReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration, cmd []string, label string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := dockerpkg.ExecOutput(execCtx, c, containerName, cmd)
		cancel()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s %q did not become ready within %s", label, containerName, timeout)
}
