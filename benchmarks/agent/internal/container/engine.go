package container

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/image"
)

// engineAPIVersion is the oldest Engine API that serves every endpoint used here,
// pinned so a newer daemon answers in the shape this client decodes.
const engineAPIVersion = "v1.41"

// Engine speaks the Docker Engine API over a unix socket. Docker Desktop, podman's
// service, Colima and OrbStack all serve it. It is safe for concurrent use.
//
// magus has no Engine-API client yet (ADR 0003 E is on hold), so this one lives with
// the benchmark until magus grows one; it never shells out to a docker CLI.
type Engine struct {
	http *http.Client
	base string
}

// Dial returns an Engine for the socket at path. It connects lazily; Platform is the
// first call that proves the socket answers.
func Dial(path string) (*Engine, error) {
	if path == "" {
		return nil, errors.New("container: name the engine socket, for example /var/run/docker.sock")
	}
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", path)
		},
	}
	return &Engine{http: &http.Client{Transport: transport}, base: "http://engine"}, nil
}

// Platform is the engine's own os/architecture in Go's spelling, which is what an
// image must match to run without emulation.
func (e *Engine) Platform(ctx context.Context) (image.Platform, error) {
	var info struct {
		OSType       string
		Architecture string
	}
	if err := e.call(ctx, http.MethodGet, "/info", nil, nil, &info); err != nil {
		return image.Platform{}, err
	}
	arch, ok := goArch[info.Architecture]
	if !ok {
		return image.Platform{}, fmt.Errorf("container: the engine reports architecture %q, which no Go build targets", info.Architecture)
	}
	return image.Platform{OS: info.OSType, Architecture: arch}, nil
}

// goArch maps the kernel's uname -m, which /info reports, to GOARCH.
var goArch = map[string]string{
	"x86_64": "amd64", "amd64": "amd64",
	"aarch64": "arm64", "arm64": "arm64",
}

// Pull fetches ref, which must pin a digest, for platform. The engine streams
// progress as JSON objects and reports a failure as one of them with the HTTP
// status still 200, so every object is read.
func (e *Engine) Pull(ctx context.Context, ref string, platform image.Platform) error {
	if !strings.Contains(ref, "@sha256:") {
		return fmt.Errorf("container: pull %s: name a digest, a tag can move", ref)
	}
	q := url.Values{"fromImage": {ref}, "platform": {platform.String()}}
	resp, err := e.do(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		err := dec.Decode(&msg)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("container: pull %s: %w", ref, err)
		}
		if msg.Error != "" {
			return fmt.Errorf("container: pull %s: %s", ref, msg.Error)
		}
	}
}

// createRequest is the subset of the Engine API's container config this package sets.
type createRequest struct {
	Image      string     `json:"Image"`
	Cmd        []string   `json:"Cmd"`
	WorkingDir string     `json:"WorkingDir,omitempty"`
	HostConfig hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	NanoCPUs int64 `json:"NanoCpus"`
	Memory   int64 `json:"Memory"`
}

func (e *Engine) create(ctx context.Context, name string, platform image.Platform, req createRequest) (string, error) {
	q := url.Values{"name": {name}, "platform": {platform.String()}}
	var out struct {
		ID string `json:"Id"`
	}
	if err := e.call(ctx, http.MethodPost, "/containers/create", q, req, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (e *Engine) start(ctx context.Context, id string) error {
	return e.call(ctx, http.MethodPost, "/containers/"+id+"/start", nil, nil, nil)
}

func (e *Engine) kill(ctx context.Context, id string) error {
	return e.call(ctx, http.MethodPost, "/containers/"+id+"/kill", nil, nil, nil)
}

func (e *Engine) remove(ctx context.Context, id string) error {
	q := url.Values{"force": {"true"}, "v": {"true"}}
	return e.call(ctx, http.MethodDelete, "/containers/"+id, q, nil, nil)
}

// putArchive extracts a tar stream into dir inside the container.
func (e *Engine) putArchive(ctx context.Context, id, dir string, tarball []byte) error {
	resp, err := e.do(ctx, http.MethodPut, "/containers/"+id+"/archive", url.Values{"path": {dir}}, bytes.NewReader(tarball))
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

type execRequest struct {
	Cmd          []string `json:"Cmd"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
}

// exec runs argv in the container, copying its two streams into stdout and stderr,
// and returns its exit code. The error is only for a failure to run it at all.
func (e *Engine) exec(ctx context.Context, id, dir string, argv []string, stdout, stderr io.Writer) (int, error) {
	var created struct {
		ID string `json:"Id"`
	}
	req := execRequest{Cmd: argv, WorkingDir: dir, AttachStdout: true, AttachStderr: true}
	if err := e.call(ctx, http.MethodPost, "/containers/"+id+"/exec", nil, req, &created); err != nil {
		return 0, err
	}
	body, err := json.Marshal(map[string]bool{"Detach": false, "Tty": false})
	if err != nil {
		return 0, err
	}
	resp, err := e.do(ctx, http.MethodPost, "/exec/"+created.ID+"/start", nil, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	err = demux(resp.Body, stdout, stderr)
	resp.Body.Close()
	if err != nil {
		return 0, fmt.Errorf("container: exec %s: %w", argv[0], err)
	}
	var inspect struct {
		Running  bool
		ExitCode int
	}
	if err := e.call(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, nil, &inspect); err != nil {
		return 0, err
	}
	if inspect.Running {
		return 0, fmt.Errorf("container: exec %s: the stream ended while the process still runs", argv[0])
	}
	return inspect.ExitCode, nil
}

// demux reads the Engine API's multiplexed stream: frames of an 8-byte header (stream
// id, three zero bytes, a big-endian uint32 length) and that many bytes of payload.
// Stream 1 is stdout and 2 is stderr.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	br := bufio.NewReader(r)
	var header [8]byte
	for {
		if _, err := io.ReadFull(br, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var out io.Writer
		switch header[0] {
		case 1:
			out = stdout
		case 2:
			out = stderr
		default:
			return fmt.Errorf("frame on stream %d: not a multiplexed exec stream", header[0])
		}
		if _, err := io.CopyN(out, br, int64(binary.BigEndian.Uint32(header[4:]))); err != nil {
			return err
		}
	}
}

func (e *Engine) call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	resp, err := e.do(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("container: %s %s: decode: %w", method, path, err)
	}
	return nil
}

// do sends one request and turns a non-2xx status into an error carrying the engine's
// own message.
func (e *Engine) do(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Response, error) {
	u := e.base + "/" + engineAPIVersion + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/x-tar")
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("container: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		var msg struct {
			Message string `json:"message"`
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if readErr != nil {
			return nil, fmt.Errorf("container: %s %s: %s, and its body did not read: %w", method, path, resp.Status, readErr)
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Message == "" {
			msg.Message = strings.TrimSpace(string(raw))
		}
		return nil, fmt.Errorf("container: %s %s: %s: %s", method, path, resp.Status, msg.Message)
	}
	return resp, nil
}
