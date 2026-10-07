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

// pollUntilReady gives up early if the container stops running, so a node
// that crashes on startup doesn't make `start` wait out the full timeout.
// label is used in the error message ("bitcoind", "lnd", etc.).
func pollUntilReady(ctx context.Context, c *client.Client, containerName string, timeout time.Duration, label string, check func(ctx context.Context) error) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := check(execCtx)
		cancel()
		if err == nil {
			return nil
		}
		running, rerr := dockerpkg.ContainerRunning(ctx, c, containerName)
		if rerr == nil && !running {
			return fmt.Errorf("%s %q exited before becoming ready (see: docker logs %s)", label, containerName, containerName)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("%s %q did not become ready within %s", label, containerName, timeout)
}

func execSucceeds(c *client.Client, containerName string, cmd []string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := dockerpkg.ExecOutput(ctx, c, containerName, cmd)
		return err
	}
}
