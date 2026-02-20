package docker

import (
	"archive/tar"
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
)

// LocalImageName returns the local image tag for a given node type and version.
func LocalImageName(nodeType, version string) string {
	return "lightnet-" + nodeType + ":" + version
}

// ImageExists reports whether the given image tag exists locally.
func ImageExists(ctx context.Context, c *client.Client, imageTag string) (bool, error) {
	_, err := c.ImageInspect(ctx, imageTag)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("inspecting image %q: %w", imageTag, err)
}

// BuildImage builds a Docker image from files embedded in fs under the nodeType/
// directory, passing buildArgName=version as a build argument.
func BuildImage(ctx context.Context, c *client.Client, embedFS embed.FS, nodeType, buildArgName, version, imageTag string) error {
	tarReader, tarWriter := io.Pipe()

	go func() {
		tw := tar.NewWriter(tarWriter)
		err := fs.WalkDir(embedFS, nodeType, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			data, err := embedFS.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading embedded file %q: %w", path, err)
			}

			// Strip the nodeType/ prefix so the Dockerfile's COPY instructions work.
			relPath := path[len(nodeType)+1:]

			hdr := &tar.Header{
				Name: relPath,
				Mode: 0o644,
				Size: int64(len(data)),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return fmt.Errorf("writing tar header for %q: %w", relPath, err)
			}
			if _, err := tw.Write(data); err != nil {
				return fmt.Errorf("writing tar content for %q: %w", relPath, err)
			}
			return nil
		})
		if err != nil {
			tarWriter.CloseWithError(err)
		} else {
			tw.Close()
			tarWriter.Close()
		}
	}()

	versionCopy := version
	buildArgs := map[string]*string{
		buildArgName: &versionCopy,
	}

	resp, err := c.ImageBuild(ctx, tarReader, client.ImageBuildOptions{
		Tags:       []string{imageTag},
		BuildArgs:  buildArgs,
		Dockerfile: "Dockerfile",
		Remove:     true,
	})
	if err != nil {
		return fmt.Errorf("building image %q: %w", imageTag, err)
	}
	defer resp.Body.Close()

	if err := jsonmessage.DisplayJSONMessagesStream(resp.Body, os.Stdout, 0, false, nil); err != nil {
		return fmt.Errorf("building image %q: %w", imageTag, err)
	}
	return nil
}
