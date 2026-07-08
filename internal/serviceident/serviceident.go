// Package serviceident derives the identity of a long-running service from its
// resolved process command, for two purposes:
//
//   - Fingerprint: the exact-match "sharing key". Two services with the same
//     fingerprint are the same instance and may be deduped/auto-shared. It is
//     deliberately conservative (default-include, explicitly-exclude): anything
//     that could make two instances behave differently is in the hash, because
//     auto-merge is the dangerous direction (merging two services that differ in a
//     load-bearing way, e.g. POSTGRES_DB, would point one project at another's
//     database).
//
//   - ClusterKey: the coarse "near-match key" (image repository + primary
//     container port). Services sharing a cluster key but not a fingerprint are
//     the sprawl foot-gun: subtly-different copies of the same service that will
//     run as separate processes. They cannot be safely auto-merged, so they are
//     surfaced to a human via [NearDuplicates].
//
// Today identity is inferred with a docker-argv heuristic ([Parse]), since the
// canonical case is a container service. A future spell-provided identity
// descriptor on types.Service will supersede the heuristic where present.
package serviceident

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Identity is the tool-aware view of a service's process command used for dedup
// and near-duplicate detection. A zero Image means the command was not recognized
// as a container run; callers fall back to the raw argv (see [Fingerprint]).
type Identity struct {
	Image string // image repository without tag, e.g. "postgres" or "docker.io/library/postgres"
	Tag   string // image tag, e.g. "15"; "" if untagged (implicit "latest") or unknown
	// Ports are the container-side published ports (not host bindings), sorted.
	// The container port is the service identity ("5432 == Postgres"); the host
	// binding is a near-ephemeral detail excluded from identity.
	Ports []string
	// Env is the declared "-e KEY=VAL" set as normalized "KEY=VAL" strings, sorted.
	// A bare "-e KEY" pass-through from the ambient environment is kept as "KEY".
	Env []string
	// Volumes are the container-side mount targets, sorted.
	Volumes []string
}

// IsContainer reports whether the command was recognized as a container run and
// therefore carries a tool-aware identity. When false, dedup falls back to the
// raw argv and the service does not participate in near-duplicate clustering.
func (i Identity) IsContainer() bool { return i.Image != "" }

// Fingerprint returns the exact-match sharing key: a hex hash of the service's
// canonical identity. Identical config yields an identical fingerprint. For a
// recognized container run the hash covers image+tag, container ports, declared
// env, and mount targets; otherwise it covers the raw process argv verbatim
// (conservative: any argv difference is a different fingerprint).
func Fingerprint(s types.Service) string {
	id := Parse(s.Command)
	h := sha256.New()
	if id.IsContainer() {
		// A stable, field-tagged serialization so two orderings of the same flags
		// hash equal (Parse sorts the repeatable groups) while distinct fields can
		// never collide across the delimiters.
		mustWrite(h, []byte("image="+id.Image+"\x00tag="+id.Tag))
		writeTagged(h, "port", id.Ports)
		writeTagged(h, "env", id.Env)
		writeTagged(h, "vol", id.Volumes)
	} else {
		mustWrite(h, []byte("argv="+s.Command.Bin))
		for _, a := range s.Command.Args {
			mustWrite(h, []byte("\x00"+a))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeTagged folds a sorted, field-tagged group into the hash.
func writeTagged(h io.Writer, tag string, vals []string) {
	mustWrite(h, []byte("\x01"+tag))
	for _, v := range vals {
		mustWrite(h, []byte("\x00"+v))
	}
}

// mustWrite writes to h, a hash.Hash whose Write is documented to never
// return an error.
func mustWrite(h io.Writer, p []byte) {
	if _, err := h.Write(p); err != nil {
		panic(err)
	}
}

// ClusterKey returns the coarse near-match key and whether the service is
// identifiable as a shareable container service. The key is (image repository +
// primary container port): image alone is too broad (two unrelated postgres roles)
// and the full config is too narrow (that is the fingerprint). The tag is
// deliberately excluded so version skew (postgres:15 vs :16) still clusters and is
// reported as a delta rather than hidden as a different service.
func ClusterKey(s types.Service) (string, bool) {
	id := Parse(s.Command)
	if !id.IsContainer() {
		return "", false
	}
	port := ""
	if len(id.Ports) > 0 {
		port = id.Ports[0]
	}
	return id.Image + "\x00" + port, true
}

// Parse extracts the tool-aware [Identity] from a process command using a
// docker-argv heuristic. It recognizes "docker run" (and "podman run") argv and
// pulls out the image, container-side ports, declared env, and mount targets,
// dropping ephemeral tokens (--name, --rm, tty and detach flags) that do not
// define the service. An unrecognized command yields a zero Identity.
func Parse(cmd types.Command) Identity {
	if !isContainerRun(cmd) {
		return Identity{}
	}
	var id Identity
	args := cmd.Args
	// Skip everything up to and including the "run" subcommand token.
	start := 0
	for i, a := range args {
		if a == "run" {
			start = i + 1
			break
		}
	}
	i := start
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			// First positional is the image; the rest is the in-container command,
			// which is not part of the service's shared identity.
			id.Image, id.Tag = splitImageTag(a)
			break
		}
		flag, inlineVal, hasInline := strings.Cut(a, "=")
		if isBooleanFlag(flag) {
			i++
			continue // e.g. --rm, -d, -it: no value, no identity
		}
		// Every non-boolean flag takes a value. docker's boolean flags are a small,
		// stable set, so treating any unknown flag as value-taking is what keeps a
		// flag's value (the "512m" in "--memory 512m") from being mistaken for the
		// image and dropping the real one.
		v := inlineVal
		if !hasInline {
			if i+1 >= len(args) {
				i++
				continue
			}
			i++
			v = args[i]
		}
		switch flag {
		case "-e", "--env":
			id.Env = append(id.Env, v)
		case "-p", "--publish":
			if cp := containerPort(v); cp != "" {
				id.Ports = append(id.Ports, cp)
			}
		case "-v", "--volume":
			if t := volumeTarget(v); t != "" {
				id.Volumes = append(id.Volumes, t)
			}
		case "--mount":
			if t := mountTarget(v); t != "" {
				id.Volumes = append(id.Volumes, t)
			}
			// Other value-taking flags (--name, --memory, ...) are consumed above and
			// ignored: not part of the shared identity.
		}
		i++
	}
	slices.Sort(id.Ports)
	slices.Sort(id.Env)
	slices.Sort(id.Volumes)
	id.Ports = slices.Compact(id.Ports)
	id.Env = slices.Compact(id.Env)
	id.Volumes = slices.Compact(id.Volumes)
	return id
}

// isContainerRun reports whether cmd looks like "docker run" / "podman run".
func isContainerRun(cmd types.Command) bool {
	return IsContainerRuntime(cmd.Bin) && slices.Contains(cmd.Args, "run")
}

// containerRuntimes are the container CLIs whose "run" this package understands.
// Shared with internal/ward via [IsContainerRuntime] so the two do not drift.
var containerRuntimes = map[string]bool{"docker": true, "podman": true, "nerdctl": true}

// IsContainerRuntime reports whether bin (by basename) is a known container CLI.
func IsContainerRuntime(bin string) bool { return containerRuntimes[Basename(bin)] }

// Basename returns the final path element of a program name.
func Basename(bin string) string {
	if i := strings.LastIndexByte(bin, '/'); i >= 0 {
		return bin[i+1:]
	}
	return bin
}

// booleanFlags are docker/podman run flags that take no value. Everything else is
// treated as value-taking (see [Parse]).
var booleanFlags = map[string]bool{
	"--rm": true, "-d": true, "--detach": true, "-i": true, "--interactive": true,
	"-t": true, "--tty": true, "--init": true, "--privileged": true, "--read-only": true,
	"-P": true, "--publish-all": true, "--no-healthcheck": true, "-q": true, "--quiet": true,
	"--oom-kill-disable": true, "--sig-proxy": true,
}

// booleanShort are the single-letter boolean flags, for classifying a combined
// short-flag block like -it or -itd.
var booleanShort = map[byte]bool{'i': true, 't': true, 'd': true, 'P': true, 'q': true}

// isBooleanFlag reports whether flag takes no value, including a combined short
// block (-it, -itd) whose every letter is a boolean short flag.
func isBooleanFlag(flag string) bool {
	if booleanFlags[flag] {
		return true
	}
	if len(flag) > 1 && flag[0] == '-' && flag[1] != '-' {
		for i := 1; i < len(flag); i++ {
			if !booleanShort[flag[i]] {
				return false
			}
		}
		return true
	}
	return false
}

// splitImageTag splits "repo:tag" into repo and tag, leaving a registry-port
// colon (host:port/path) attached to the repo. A digest ("repo@sha256:...") keeps
// the digest as the tag so pinned images stay distinct.
func splitImageTag(ref string) (repo, tag string) {
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		return ref[:at], ref[at+1:]
	}
	colon := strings.LastIndexByte(ref, ':')
	if colon < 0 {
		return ref, ""
	}
	// A colon that precedes a "/" is a registry port, not a tag separator.
	if strings.IndexByte(ref[colon:], '/') >= 0 {
		return ref, ""
	}
	return ref[:colon], ref[colon+1:]
}

// containerPort extracts the container-side port from a "-p" spec, which may be
// "CONTAINER", "HOST:CONTAINER", or "IP:HOST:CONTAINER", each optionally
// "/proto". The container port is always the last colon-separated segment.
func containerPort(spec string) string {
	seg := spec
	if slash := strings.IndexByte(seg, '/'); slash >= 0 {
		seg = seg[:slash]
	}
	if colon := strings.LastIndexByte(seg, ':'); colon >= 0 {
		seg = seg[colon+1:]
	}
	return seg
}

// volumeTarget extracts the container-side mount target from a "-v" spec:
// "TARGET" (anonymous), "SOURCE:TARGET", or "SOURCE:TARGET:opts". A trailing
// mount-options token (ro, rw, z, Z, cached, delegated, consistent) is dropped.
func volumeTarget(spec string) string {
	fields := strings.Split(spec, ":")
	// Drop a trailing options token so "/host:/data:ro" targets "/data".
	if n := len(fields); n >= 3 && isMountOpts(fields[n-1]) {
		fields = fields[:n-1]
	}
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0] // anonymous volume: the target itself
	default:
		return fields[len(fields)-1]
	}
}

// mountTarget extracts the "target="/"dst="/"destination=" value from a
// "--mount" comma-separated key=value spec.
func mountTarget(spec string) string {
	for _, kv := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "target", "dst", "destination":
			return v
		}
	}
	return ""
}

// isMountOpts reports whether tok is a docker volume mount-options token.
func isMountOpts(tok string) bool {
	switch tok {
	case "ro", "rw", "z", "Z", "cached", "delegated", "consistent", "nocopy":
		return true
	}
	return false
}
