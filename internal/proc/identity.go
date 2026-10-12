package proc

import (
	"bytes"
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
)

// This file computes the ADOPTION IDENTITY: the version string a magus build sends over
// the wire and the server compares against to decide whether to adopt a forwarded call.
// It is distinct from the human-facing DISPLAY version (`magus --version`, the server's
// StatusReply.Version): display stays friendly, only the gate uses the fingerprint.
//
// The problem it solves: an unstamped dev build carries a fixed placeholder version
// (devVersionSentinel), so the server gate (a plain string equality) treats EVERY dev
// build as identical to every other. A stale dev server left running from an old, since-
// deleted binary would then adopt runs from an unrelated newer dev client and execute
// them with the wrong code. Fingerprinting each dev build from its embedded VCS stamp
// makes builds of different revisions (and every dirty build) refuse to adopt each other.

// devVersionSentinel is the placeholder main.version carries in an unstamped dev build:
// `go build`/`go run ./cmd/magus` without `-ldflags "-X main.version=..."`. Every RELEASE
// build is stamped with a real version (git describe) by the magusfile, so any value
// other than this exact string is a stamped release and passes the gate on its version as
// before. Keep in sync with the default of `version` in cmd/magus/version.go.
const devVersionSentinel = "unknown"

// devUnverifiable is a per-PROCESS-unique adoption identity, minted once at startup. It is
// the identity a dev build falls back to when it cannot PROVE what code it is running: a
// dirty working tree, or a build with no embedded VCS info. Every process gets a different
// token, so two such builds never compare equal and adoption between them is always
// refused. This is the fail-closed direction: two dirty trees at the same revision are not
// provably the same bytes, and a since-rebuilt server must not be trusted to run a client's
// code, so we decline rather than risk executing stale logic.
var devUnverifiable = "dev-unverifiable-" + randomToken()

// binaryIdentity identifies a MODIFIED-tree build by the executable file it runs from:
// its resolved path plus size and mtime, hashed. Same file, same identity (so a server
// and the nested magus it forked still adopt each other in a dirty tree), while a
// rebuilt, replaced, or borrowed binary mismatches and the caller runs locally instead.
//
// Captured at process start, not lazily: a server whose executable is rebuilt underneath
// it must keep the identity of the file it actually loaded, or it would stat the NEW
// file, match the new client, and execute the run with the old code: the exact stale
// adoption this identity exists to refuse.
var binaryIdentity = computeBinaryIdentity()

func computeBinaryIdentity() string {
	exe, err := os.Executable()
	if err != nil {
		return devUnverifiable // cannot prove which file runs: fail closed per process
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return devUnverifiable
	}
	h := sha256.Sum256([]byte(exe + "\x00" +
		strconv.FormatInt(fi.Size(), 10) + "\x00" +
		strconv.FormatInt(fi.ModTime().UnixNano(), 10)))
	return "dev-bin-" + hex.EncodeToString(h[:8])
}

func randomToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// rand.Read essentially never fails; if it somehow did, returning a constant would
		// make distinct unverifiable builds compare equal and adopt each other (the exact
		// bug this guards against), so fail loudly instead.
		panic("proc: read random bytes for build identity: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// adoptionIdentity maps a build's DISPLAY version to the identity used for the server
// adoption gate. Client and server both run it over their own build, so two builds adopt
// each other only when their identities are equal. It is total and deterministic per
// process:
//
//   - "" passes through unchanged. An empty identity on either side disables the gate; it
//     is the test-injection / pre-versioning escape hatch (see service.versionAdmits).
//   - A MODIFIED-tree build maps to binaryIdentity whatever its display version says. A
//     stamped `git describe --dirty` string is shared by every dirty build of one commit,
//     so trusting it let a stale server adopt a since-rebuilt client and execute the run
//     with old code; the executable-file identity admits only the byte-identical binary.
//   - A stamped release version (anything but devVersionSentinel) passes through unchanged,
//     so releases keep matching on their exact version.
//   - The dev sentinel is replaced by a build fingerprint: "dev-<revision>" for a CLEAN
//     build carrying VCS info (two clean builds of one commit are provably the same code
//     and DO adopt each other, preserving the run-a-server-then-adopt-into-it workflow),
//     or the per-process devUnverifiable token for a VCS-less build (never matches:
//     adoption refused).
//
// Embedding the fingerprint IN the version string (rather than adding a new wire field) is
// deliberate and load-bearing: a stale PRE-FIX server compares the string it receives
// against its own stored "unknown" and correctly mismatches, refusing the call. A new wire
// field would be silently ignored by that old server and fail OPEN: the very failure mode
// this fix exists to close.
func adoptionIdentity(displayVersion string) string {
	if displayVersion == "" {
		return "" // the check-disabled / test-injection escape hatch stays open
	}
	rev, modified, ok := buildVCS()
	if ok && modified {
		return binaryIdentity
	}
	if displayVersion != devVersionSentinel {
		return displayVersion
	}
	if !ok || rev == "" {
		return devUnverifiable
	}
	return "dev-" + rev
}

// buildVCS reads the VCS stamp the Go toolchain embeds in the binary at build time (Go
// 1.18+). ok is false only when no build info is available at all (e.g. a binary built
// with -ldflags that strips it in an unusual way); rev and modified are the vcs.revision
// and vcs.modified settings, which are absent ("" / false) when the build carried no VCS
// info at all: built with -buildvcs=false, or outside a VCS work tree. A var so tests
// can pin the build state instead of inheriting whatever tree built the test binary.
var buildVCS = func() (rev string, modified bool, ok bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified, true
}

// BinaryBuild identifies the build of the running executable: a 16-hex digest of its Go
// build ID, which the toolchain derives from the build's inputs, so two builds of the same
// source share it and any other build does not, dirty tree or not. An executable whose
// build ID cannot be read falls back to binaryIdentity, which only separates builds
// further. Never empty: a cache key can carry it, a store directory cannot (see
// [BinaryDigest]).
func BinaryBuild() string { return cmp.Or(BinaryDigest(), binaryIdentity) }

// BinaryDigest is BinaryBuild's digest of the Go build ID alone: "" when the executable
// carries none or cannot be read. It names a build only by what the toolchain derived
// from its inputs, so a caller that stores something per build uses it and stores
// nothing when it is empty, rather than keying on a per-file identity that gives every
// rebuilt or copied binary a store of its own.
//
// Read once per process, like binaryIdentity and for the same reason: a file rebuilt
// underneath this process must not speak for the code it loaded. A func rather than the
// OnceValue itself so no caller can reassign it.
func BinaryDigest() string { return binaryDigest() }

var binaryDigest = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	digest, err := buildIDDigest(exe)
	if err != nil {
		return ""
	}
	return digest
})

// buildIDDigest is BinaryBuild's digest of exe's build ID: "" with no error when exe
// carries none.
func buildIDDigest(exe string) (string, error) {
	id, err := readBuildID(exe)
	if err != nil || id == "" {
		return "", err
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:16], nil
}

// readBuildID reads the build ID the Go linker writes at the start of the text segment,
// which lies in the file's first 32 KiB (cmd/internal/buildid reads the same window), or
// from ELF's .note.go.buildid when an external link moved text. "" with no error when exe
// is readable and carries none; an error when it cannot be read or its note is malformed.
func readBuildID(exe string) (string, error) {
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 32<<10)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", err
	}
	const marker = "\xff Go build ID: \""
	if i := bytes.Index(head[:n], []byte(marker)); i >= 0 {
		rest := head[i+len(marker) : n]
		if j := bytes.IndexByte(rest, '"'); j > 0 {
			return string(rest[:j]), nil
		}
	}
	ef, err := elf.NewFile(f)
	var notELF *elf.FormatError
	if errors.As(err, &notELF) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	note := ef.Section(".note.go.buildid")
	if note == nil {
		return "", nil
	}
	// namesz, descsz, type, then "Go\x00\x00" and the ID.
	data, err := note.Data()
	if err != nil {
		return "", err
	}
	if len(data) < 16 {
		return "", fmt.Errorf("truncated .note.go.buildid in %s", exe)
	}
	size := int(ef.ByteOrder.Uint32(data[4:8]))
	if len(data) < 16+size {
		return "", fmt.Errorf("truncated .note.go.buildid in %s", exe)
	}
	return string(data[16 : 16+size]), nil
}
