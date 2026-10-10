package vcs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// hgOpenObjectBatch starts one `serve --cmdserver pipe`. Each Read is a
// `cat` on that process. A cat per path is a process per file, which is the
// cost git's cat-file batch already avoids.
func hgOpenObjectBatch(ctx context.Context, prog, root, rev string) (ObjectBatch, error) {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return nil, err
	}
	cmd := vcsExec(ctx, prog, "serve", "--cmdserver", "pipe")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s cmdserver: %w", prog, err)
	}
	b := &cmdBatch{prog: prog, rev: rev, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &stderr}
	ch, hello, err := b.message()
	if err != nil || ch != 'o' || !bytes.Contains(hello, []byte("runcommand")) {
		_ = b.Close()
		if err == nil {
			err = fmt.Errorf("hello %q", hello)
		}
		return nil, fmt.Errorf("%s cmdserver: %w", prog, err)
	}
	return b, nil
}

// cmdBatch speaks the Mercurial command-server protocol: a channel byte, a
// big-endian length, then that many bytes. 'o' is stdout, 'e' is stderr, and
// 'r' ends the command with a big-endian int32 exit code.
type cmdBatch struct {
	prog   string
	rev    string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bytes.Buffer
	// err is the failure that left the stream out of step with its commands.
	// Every later Read returns it: the next reply would answer an earlier command.
	err error
}

// cmdResult is one command's reply on the command server.
type cmdResult struct {
	code           int
	stdout, stderr string
}

func (b *cmdBatch) Read(rel string) (string, error) {
	if b.err != nil {
		return "", b.err
	}
	if strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("%s cat: path contains a NUL", b.prog)
	}
	res, err := b.run("cat", "-r", b.rev, "--", rel)
	if err != nil {
		b.err = fmt.Errorf("%s cat: %w", b.prog, err)
		return "", b.err
	}
	if res.code != 0 {
		if strings.Contains(res.stderr, "no such file") {
			return "", fmt.Errorf("%s cat: %s: %w", b.prog, rel, ErrObjectMissing)
		}
		msg := strings.TrimSpace(res.stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.stdout)
		}
		return "", fmt.Errorf("%s cat: %s", b.prog, msg)
	}
	return res.stdout, nil
}

func (b *cmdBatch) run(args ...string) (cmdResult, error) {
	var payload []byte
	for i, a := range args {
		if i > 0 {
			payload = append(payload, 0)
		}
		payload = append(payload, a...)
	}
	if _, err := io.WriteString(b.stdin, "runcommand\n"); err != nil {
		return cmdResult{}, err
	}
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(payload)))
	if _, err := b.stdin.Write(n[:]); err != nil {
		return cmdResult{}, err
	}
	if _, err := b.stdin.Write(payload); err != nil {
		return cmdResult{}, err
	}
	var stdout, stderr bytes.Buffer
	for {
		ch, data, err := b.message()
		if err != nil {
			return cmdResult{}, err
		}
		switch ch {
		case 'o':
			_, _ = stdout.Write(data)
		case 'e':
			_, _ = stderr.Write(data)
		case 'r':
			if len(data) != 4 {
				return cmdResult{}, fmt.Errorf("exit %q", data)
			}
			code := int(int32(binary.BigEndian.Uint32(data)))
			return cmdResult{code: code, stdout: stdout.String(), stderr: stderr.String()}, nil
		default:
			// The protocol lets a client skip an unknown lowercase channel; an
			// uppercase one asks for input, and there is none to give.
			if ch >= 'a' && ch <= 'z' {
				continue
			}
			return cmdResult{}, fmt.Errorf("channel %q", ch)
		}
	}
}

func (b *cmdBatch) message() (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(b.stdout, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(b.stdout, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}

func (b *cmdBatch) Close() error {
	err := b.stdin.Close()
	_, _ = io.Copy(io.Discard, b.stdout)
	waitErr := b.cmd.Wait()
	if err != nil {
		return err
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(b.stderr.String()); msg != "" {
			return fmt.Errorf("%s cmdserver: %s: %w", b.prog, msg, waitErr)
		}
		return fmt.Errorf("%s cmdserver: %w", b.prog, waitErr)
	}
	return nil
}
