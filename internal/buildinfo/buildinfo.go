package buildinfo

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
	"sync"
)

// commit and commitDate are the linker's stamp, set by go-build:
//
//	go build -ldflags "-X github.com/egladman/magus/internal/buildinfo.commit=abc123 ..."
var (
	commit     = Unknown
	commitDate = Unknown
)

// Unknown is a stamp field no build set.
const Unknown = "unknown"

// Commit is the revision this binary was built from and that commit's date, RFC 3339,
// each [Unknown] when nothing stamped it. A bare `go build` passes no -X flags, so they
// come from the VCS stamp the toolchain embeds instead. go-build's -X values win when
// present: its sandboxed link embeds a VCS stamp from another revision.
func Commit() (id, date string) {
	if commit != Unknown {
		return commit, commitDate
	}
	id, date = Unknown, Unknown
	for _, s := range buildSettings() {
		switch s.Key {
		case "vcs.revision":
			id = s.Value
		case "vcs.time":
			date = s.Value
		}
	}
	return id, date
}

// Modified reports whether the VCS stamp says the tree held uncommitted changes. Only a
// bare `go build` carries one; go-build marks a dirty tree in its version instead.
func Modified() bool {
	for _, s := range buildSettings() {
		if s.Key == "vcs.modified" {
			return s.Value == "true"
		}
	}
	return false
}

func buildSettings() []debug.BuildSetting {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info.Settings
}

// unverifiable is a per-PROCESS-unique identity, minted once at startup, for a build that
// cannot PROVE what code it is running. Every process gets a different token, so two such
// builds never compare equal: two dirty trees at the same revision are not provably the
// same bytes, and a since-rebuilt server must not be trusted to run a client's code.
var unverifiable = "dev-unverifiable-" + randomToken()

// file identifies the executable file this process runs from: its resolved path plus size
// and mtime, hashed.
//
// Captured at process start, not lazily: a server whose executable is rebuilt underneath
// it must keep the identity of the file it actually loaded, or it would stat the NEW
// file, match the new client, and execute the run with the old code.
var file = computeFile()

// Unverifiable is the per-process identity of a build that cannot prove what code it
// runs. It never equals another process's.
func Unverifiable() string { return unverifiable }

// File identifies the executable file this process loaded, read once at startup: the same
// file gives the same identity, so a process and the children it forks from that file
// share it, while a rebuilt, replaced or borrowed binary does not. A file that cannot be
// read falls back to [Unverifiable]. Never empty.
func File() string { return file }

func computeFile() string {
	exe, err := os.Executable()
	if err != nil {
		return unverifiable
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return unverifiable
	}
	h := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", exe, fi.Size(), fi.ModTime().UnixNano())))
	return "dev-bin-" + hex.EncodeToString(h[:8])
}

func randomToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// A constant would make distinct unverifiable builds compare equal and adopt each
		// other, so fail loudly instead.
		panic("buildinfo: read random bytes for build identity: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// Build identifies the build of the running executable: a 16-hex digest of its Go build
// ID, which the toolchain derives from the build's inputs, so two builds of the same
// source share it and any other build does not, dirty tree or not. An executable whose
// build ID cannot be read falls back to [File], which only separates builds further.
// Never empty: a cache key can carry it, a store directory cannot (see [Digest]).
func Build() string { return cmp.Or(Digest(), file) }

// Digest is Build's digest of the Go build ID alone: "" when the executable carries none
// or cannot be read. It names a build only by what the toolchain derived from its inputs,
// so a caller that stores something per build uses it and stores nothing when it is
// empty, rather than keying on a per-file identity that gives every rebuilt or copied
// binary a store of its own.
//
// Read once per process, like File and for the same reason: a file rebuilt underneath
// this process must not speak for the code it loaded. A func rather than the OnceValue
// itself so no caller can reassign it.
func Digest() string { return digest() }

var digest = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	d, err := buildIDDigest(exe)
	if err != nil {
		return ""
	}
	return d
})

// buildIDDigest is Build's digest of exe's build ID: "" with no error when exe carries
// none.
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
