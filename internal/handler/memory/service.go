// Package memory is the console-facing MemoryService handler: an observable and EDITABLE
// view over the durable magus_memory files (status, progress, decisions). It is a SECOND
// door onto the EXACT on-disk files the MCP magus_memory tool maintains - it resolves the
// per-repository memory directory through mcp.MemoryDir, so the browser edit surface and
// the agent-facing tool share one set of files, never a second store. Its reason to
// exist: agent memory is append-heavy and never rotated by default, so it grows unbounded
// and an agent can silently bloat it; a human list/read/edit/delete surface is the safety
// valve.
//
// The content it returns is AGENT-WRITTEN and therefore UNTRUSTED: clients must render it
// as text/markdown, never as trusted HTML. The daemon mounts it on the loopback listener
// behind the same bearer guard as the other console services and never on the LAN share
// listener - memory is the operator's own working notes, not a shared read surface.
package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/handler/mcp"
	memoryv1 "github.com/egladman/magus/proto/gen/go/magus/memory/v1"
	"github.com/egladman/magus/proto/gen/go/magus/memory/v1/memoryv1connect"
)

// workspace is the narrow slice of *magus.Magus the handler needs: the workspace root,
// which keys the per-repository memory directory. Satisfied structurally by *magus.Magus.
type workspace interface {
	Root() string
}

// Service implements memoryv1connect.MemoryServiceHandler over the real on-disk memory
// files. dir is injectable so the file mapping is unit-testable without a live workspace;
// it defaults to resolving the per-repository memory directory the MCP tool uses.
type Service struct {
	ws  workspace
	dir func(root string) (string, error)
}

// NewService builds a MemoryService handler over the workspace ws.
func NewService(ws workspace) *Service {
	return &Service{ws: ws, dir: mcp.MemoryDir}
}

var _ memoryv1connect.MemoryServiceHandler = (*Service)(nil)

// memoryFiles is the closed set of durable files, in a stable display order (snapshot
// first, then the two journals). It mirrors the MCP tool's file set; each maps to a
// "<name>.md" basename under the memory directory. Adding one here is an API decision.
var memoryFiles = []struct {
	file memoryv1.MemoryFile
	base string
}{
	{memoryv1.MemoryFile_MEMORY_FILE_STATUS, "status.md"},
	{memoryv1.MemoryFile_MEMORY_FILE_PROGRESS, "progress.md"},
	{memoryv1.MemoryFile_MEMORY_FILE_DECISIONS, "decisions.md"},
}

// basename maps a MemoryFile to its on-disk basename, rejecting UNSPECIFIED and any
// unknown value so a caller can never target a path outside the closed set.
func basename(f memoryv1.MemoryFile) (string, error) {
	for _, m := range memoryFiles {
		if m.file == f {
			return m.base, nil
		}
	}
	return "", fmt.Errorf("memory: unknown or unspecified file %v", f)
}

// ListMemory returns metadata (never content) for every known file plus the on-disk
// directory holding them.
func (s *Service) ListMemory(_ context.Context, _ *connect.Request[memoryv1.ListMemoryRequest]) (*connect.Response[memoryv1.ListMemoryResponse], error) {
	dir, err := s.dir(s.ws.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	docs := make([]*memoryv1.MemoryDoc, 0, len(memoryFiles))
	for _, m := range memoryFiles {
		doc, derr := statDoc(dir, m.file, m.base)
		if derr != nil {
			return nil, connect.NewError(connect.CodeInternal, derr)
		}
		docs = append(docs, doc)
	}
	return connect.NewResponse(&memoryv1.ListMemoryResponse{Docs: docs, Dir: dir}), nil
}

// GetMemory returns one file's raw markdown content and metadata. A file that does not
// exist yet returns an empty doc with exists=false, not an error.
func (s *Service) GetMemory(_ context.Context, req *connect.Request[memoryv1.GetMemoryRequest]) (*connect.Response[memoryv1.GetMemoryResponse], error) {
	base, dir, err := s.resolve(req.Msg.GetFile())
	if err != nil {
		return nil, err
	}
	doc, err := readDoc(dir, req.Msg.GetFile(), base)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.GetMemoryResponse{Doc: doc}), nil
}

// PutMemory replaces one file's whole content, creating it if absent. It writes atomically
// (temp file plus rename in the same directory) so a crash mid-write leaves either the old
// content or the new, never a truncated file.
func (s *Service) PutMemory(_ context.Context, req *connect.Request[memoryv1.PutMemoryRequest]) (*connect.Response[memoryv1.PutMemoryResponse], error) {
	base, dir, err := s.resolve(req.Msg.GetFile())
	if err != nil {
		return nil, err
	}
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("memory: create dir: %w", mkErr))
	}
	if wErr := writeFileAtomic(filepath.Join(dir, base), []byte(req.Msg.GetContent())); wErr != nil {
		return nil, connect.NewError(connect.CodeInternal, wErr)
	}
	doc, err := readDoc(dir, req.Msg.GetFile(), base)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.PutMemoryResponse{Doc: doc}), nil
}

// DeleteMemory removes one file from disk. Deleting an already-absent file succeeds and
// reports exists=false.
func (s *Service) DeleteMemory(_ context.Context, req *connect.Request[memoryv1.DeleteMemoryRequest]) (*connect.Response[memoryv1.DeleteMemoryResponse], error) {
	base, dir, err := s.resolve(req.Msg.GetFile())
	if err != nil {
		return nil, err
	}
	if rmErr := os.Remove(filepath.Join(dir, base)); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("memory: delete: %w", rmErr))
	}
	doc, err := readDoc(dir, req.Msg.GetFile(), base)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.DeleteMemoryResponse{Doc: doc}), nil
}

// resolve validates the requested file and returns its basename and the memory directory.
// An UNSPECIFIED or unknown file is an InvalidArgument, so the mutating RPCs reject a
// malformed request before touching the filesystem.
func (s *Service) resolve(f memoryv1.MemoryFile) (base, dir string, err error) {
	base, berr := basename(f)
	if berr != nil {
		return "", "", connect.NewError(connect.CodeInvalidArgument, berr)
	}
	dir, derr := s.dir(s.ws.Root())
	if derr != nil {
		return "", "", connect.NewError(connect.CodeInternal, derr)
	}
	return base, dir, nil
}

// statDoc builds a metadata-only MemoryDoc (no content) for the file at dir/base.
func statDoc(dir string, f memoryv1.MemoryFile, base string) (*memoryv1.MemoryDoc, error) {
	doc := &memoryv1.MemoryDoc{File: f, Name: base}
	info, err := os.Stat(filepath.Join(dir, base))
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: stat %s: %w", base, err)
	}
	doc.Exists = true
	doc.SizeBytes = info.Size()
	doc.Modified = timestamppb.New(info.ModTime())
	return doc, nil
}

// readDoc builds a full MemoryDoc (with content) for the file at dir/base. A missing file
// yields an empty doc with exists=false.
func readDoc(dir string, f memoryv1.MemoryFile, base string) (*memoryv1.MemoryDoc, error) {
	path := filepath.Join(dir, base)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &memoryv1.MemoryDoc{File: f, Name: base}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: read %s: %w", base, err)
	}
	doc := &memoryv1.MemoryDoc{File: f, Name: base, Content: string(b), Exists: true, SizeBytes: int64(len(b))}
	if info, serr := os.Stat(path); serr == nil {
		doc.Modified = timestamppb.New(info.ModTime())
	}
	return doc, nil
}

// writeFileAtomic writes content to path via a temp file in the same directory and a
// rename, so a concurrent reader (or a crash) never sees a half-written file.
func writeFileAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("memory: write temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, werr := tmp.Write(content); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("memory: write: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory: write: %w", cerr)
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		os.Remove(tmpName)
		return fmt.Errorf("memory: replace: %w", rerr)
	}
	return nil
}
