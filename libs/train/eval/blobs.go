package eval

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Blobs reads files of one git revision through a single `git cat-file --batch`, so
// reading thousands of files costs one process. Blobs read at a revision make a build
// independent of the working tree.
type Blobs struct {
	rev string
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

// OpenBlobs starts the reader in root's repository.
func OpenBlobs(root, rev string) (*Blobs, error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git cat-file: %w", err)
	}
	return &Blobs{rev: rev, cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}, nil
}

// Read returns the blob sha and bytes of path at the revision.
func (b *Blobs) Read(path string) (string, []byte, error) {
	if _, err := fmt.Fprintf(b.in, "%s:%s\n", b.rev, path); err != nil {
		return "", nil, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	fields := strings.Fields(header)
	if len(fields) == 2 && fields[1] == "missing" {
		return "", nil, fmt.Errorf("%s: not in %s", path, b.rev)
	}
	if len(fields) != 3 || fields[1] != "blob" {
		return "", nil, fmt.Errorf("%s at %s: git answered %q", path, b.rev, strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", nil, err
	}
	data := make([]byte, size+1)
	if _, err := io.ReadFull(b.out, data); err != nil {
		return "", nil, err
	}
	return fields[0], data[:size], nil
}

// Close stops the reader.
func (b *Blobs) Close() error {
	_ = b.in.Close()
	return b.cmd.Wait()
}
