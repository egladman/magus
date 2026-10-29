package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
)

// testbedDir is where every SWE-bench image holds the task's checkout.
const testbedDir = "/testbed"

// limits are the resources a trial container gets. Both are required: a trial left
// to the engine's defaults gets whatever the machine has, and its wall time compares
// with nothing.
type limits struct {
	CPUs        float64 `json:"cpus"`
	MemoryBytes int64   `json:"memory_bytes"`
}

// instance is one running trial or grading container. Its methods are safe for
// concurrent use; Remove ends it.
type instance struct {
	engine *engineClient
	id     string
	Name   string
	Image  pinnedImage
	limits limits
}

// createInstance creates and starts a container named name from img, already pulled, under
// lim. It idles until Remove, so every step runs in it through Exec.
func createInstance(ctx context.Context, e *engineClient, name string, img pinnedImage, lim limits) (*instance, error) {
	if lim.CPUs <= 0 || lim.MemoryBytes <= 0 {
		return nil, fmt.Errorf("container: %s: set both a CPU and a memory limit, got %g CPUs and %d bytes", name, lim.CPUs, lim.MemoryBytes)
	}
	id, err := e.create(ctx, name, img.Platform, createRequest{
		Image:      img.Pinned(),
		Cmd:        []string{"sleep", "infinity"},
		WorkingDir: testbedDir,
		HostConfig: hostConfig{NanoCPUs: int64(lim.CPUs * 1e9), Memory: lim.MemoryBytes},
	})
	if err != nil {
		return nil, err
	}
	c := &instance{engine: e, id: id, Name: name, Image: img, limits: lim}
	if err := e.start(ctx, id); err != nil {
		return nil, errors.Join(err, c.Remove(context.WithoutCancel(ctx)))
	}
	return c, nil
}

// Exec runs argv in testbedDir and returns its exit code; a non-zero code is not an
// error. stdout and stderr may be the same writer, which then holds both streams in
// the order the process wrote them.
func (c *instance) Exec(ctx context.Context, argv []string, stdout, stderr io.Writer) (int, error) {
	return c.engine.exec(ctx, c.id, testbedDir, argv, stdout, stderr)
}

// WriteFile writes data to the absolute path name with mode perm, creating missing
// parent directories.
func (c *instance) WriteFile(ctx context.Context, name string, data []byte, perm fs.FileMode) error {
	if !strings.HasPrefix(name, "/") {
		return fmt.Errorf("container: write %s: the path must be absolute", name)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: strings.TrimPrefix(name, "/"), Mode: int64(perm), Size: int64(len(data)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return c.engine.putArchive(ctx, c.id, "/", buf.Bytes())
}

// Remove kills and deletes the container with its anonymous volumes.
func (c *instance) Remove(ctx context.Context) error {
	return c.engine.remove(ctx, c.id)
}

// run is Exec for a step whose non-zero exit is a failure.
func (c *instance) run(ctx context.Context, argv []string, out io.Writer) error {
	code, err := c.Exec(ctx, argv, out, out)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("container: %s exited %d", strings.Join(argv, " "), code)
	}
	return nil
}
