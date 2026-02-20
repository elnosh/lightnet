package docker

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// Exec runs a command inside a container, streaming stdout/stderr to the terminal.
func Exec(ctx context.Context, c *client.Client, containerName string, cmd []string) error {
	execResp, err := c.ExecCreate(ctx, containerName, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("creating exec in %q: %w", containerName, err)
	}

	attach, err := c.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("attaching to exec in %q: %w", containerName, err)
	}
	defer attach.Close()

	// Docker multiplexes stdout/stderr over a single stream with an 8-byte header.
	// StdCopy demultiplexes it correctly.
	if _, err := stdcopy.StdCopy(os.Stdout, os.Stderr, attach.Reader); err != nil && err != io.EOF {
		return fmt.Errorf("streaming exec output: %w", err)
	}

	// Check exit code
	inspect, err := c.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return fmt.Errorf("inspecting exec: %w", err)
	}
	if inspect.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d", inspect.ExitCode)
	}

	return nil
}

// ExecOutput runs a command inside a container and returns combined stdout output as a string.
func ExecOutput(ctx context.Context, c *client.Client, containerName string, cmd []string) (string, error) {
	execResp, err := c.ExecCreate(ctx, containerName, client.ExecCreateOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("creating exec in %q: %w", containerName, err)
	}

	attach, err := c.ExecAttach(ctx, execResp.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", fmt.Errorf("attaching to exec in %q: %w", containerName, err)
	}
	defer attach.Close()

	pr, pw := io.Pipe()
	go func() {
		stdcopy.StdCopy(pw, pw, attach.Reader)
		pw.Close()
	}()

	out, err := io.ReadAll(pr)
	if err != nil {
		return "", fmt.Errorf("reading exec output: %w", err)
	}

	// Check exit code so callers can distinguish success from failure.
	inspect, err := c.ExecInspect(ctx, execResp.ID, client.ExecInspectOptions{})
	if err != nil {
		return "", fmt.Errorf("inspecting exec: %w", err)
	}
	if inspect.ExitCode != 0 {
		return string(out), fmt.Errorf("command exited with code %d: %s", inspect.ExitCode, string(out))
	}

	return string(out), nil
}
