// Package memory is the console-facing MemoryService handler: an observable, editable view
// over the durable agent-memory RECORDS the MCP magus_memory tool writes. It is a SECOND
// door onto the EXACT on-disk store that tool maintains (internal/memory, aliased `store`
// here), never a second store of its own, so the browser edit surface and the agent-facing
// tool share one set of records. Its reason to exist: an agent can accumulate stale or
// bloated records, and a human list/edit/delete surface is the safety valve.
//
// A record's body/refs are AGENT-WRITTEN and therefore UNTRUSTED: clients must render them
// as text, never as trusted HTML. The daemon mounts this on the loopback listener behind
// the same bearer guard as the other console services and never on the LAN share listener.
package memory

import (
	"context"
	"errors"
	"os"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	store "github.com/egladman/magus/internal/memory"
	memoryv1 "github.com/egladman/magus/proto/gen/go/magus/memory/v1"
	"github.com/egladman/magus/proto/gen/go/magus/memory/v1/memoryv1connect"
)

// workspace is the narrow slice of *magus.Magus the handler needs: the workspace root,
// which keys the per-repository record store. Satisfied structurally by *magus.Magus.
type workspace interface {
	Root() string
}

// Service implements memoryv1connect.MemoryServiceHandler over the on-disk record store.
type Service struct {
	ws workspace
}

// NewService builds a MemoryService handler over the workspace ws.
func NewService(ws workspace) *Service { return &Service{ws: ws} }

var _ memoryv1connect.MemoryServiceHandler = (*Service)(nil)

// ListMemories returns every record in full. Pagination is wired in the contract but the
// store returns all records today, so next_page_token is always empty.
func (s *Service) ListMemories(_ context.Context, _ *connect.Request[memoryv1.ListMemoriesRequest]) (*connect.Response[memoryv1.ListMemoriesResponse], error) {
	recs, err := store.List(s.ws.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*memoryv1.Memory, len(recs))
	for i, r := range recs {
		out[i] = recordToProto(r)
	}
	return connect.NewResponse(&memoryv1.ListMemoriesResponse{Memories: out}), nil
}

// UpdateMemory upserts a record keyed by memory.name. allow_missing=true creates the record
// when absent (the upsert); allow_missing=false rejects an absent record with NotFound. Only
// a full replace is supported, so a non-empty update_mask is rejected rather than silently
// dropping the fields the mask omits.
func (s *Service) UpdateMemory(_ context.Context, req *connect.Request[memoryv1.UpdateMemoryRequest]) (*connect.Response[memoryv1.UpdateMemoryResponse], error) {
	if paths := req.Msg.GetUpdateMask().GetPaths(); len(paths) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("memory: partial update_mask is not supported; send the full record"))
	}
	root := s.ws.Root()
	rec := recordFromProto(req.Msg.GetMemory())
	// Validate up front so a schema violation is an honest InvalidArgument, distinct from the
	// storage failures Put can also return (which are Internal below).
	if err := store.Validate(rec); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if !req.Msg.GetAllowMissing() {
		if _, err := store.Get(root, rec.Name); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("memory: no record named "+rec.Name+" (set allow_missing to create it)"))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	stored, err := store.Put(root, rec) // validation already passed, so an error here is storage
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.UpdateMemoryResponse{Memory: recordToProto(stored)}), nil
}

// DeleteMemory removes a record by name. allow_missing=true makes deleting an absent record
// a no-op; otherwise an absent record is NotFound. Any other failure is a storage error.
func (s *Service) DeleteMemory(_ context.Context, req *connect.Request[memoryv1.DeleteMemoryRequest]) (*connect.Response[memoryv1.DeleteMemoryResponse], error) {
	err := store.Delete(s.ws.Root(), req.Msg.GetName(), req.Msg.GetAllowMissing())
	if errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.DeleteMemoryResponse{}), nil
}

// GetCursor returns the cursor snapshot, empty when never written.
func (s *Service) GetCursor(_ context.Context, _ *connect.Request[memoryv1.GetCursorRequest]) (*connect.Response[memoryv1.GetCursorResponse], error) {
	content, err := store.ReadCursor(s.ws.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.GetCursorResponse{Content: content}), nil
}

// UpdateCursor overwrites the cursor snapshot.
func (s *Service) UpdateCursor(_ context.Context, req *connect.Request[memoryv1.UpdateCursorRequest]) (*connect.Response[memoryv1.UpdateCursorResponse], error) {
	content := req.Msg.GetContent()
	if err := store.WriteCursor(s.ws.Root(), content); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.UpdateCursorResponse{Content: content}), nil
}

// recordToProto maps a stored record to the wire message, stamping the output-only
// timestamps from the store's unix seconds.
func recordToProto(r store.Record) *memoryv1.Memory {
	refs := make([]*memoryv1.MemoryRef, len(r.Refs))
	for i, ref := range r.Refs {
		refs[i] = &memoryv1.MemoryRef{Kind: refKindToProto(ref.Kind), Target: ref.Target}
	}
	m := &memoryv1.Memory{
		Name: r.Name, Type: typeToProto(r.Type), Refs: refs,
		Status: r.Status, Body: r.Body, References: r.References,
	}
	if r.Created > 0 {
		m.CreateTime = timestamppb.New(time.Unix(r.Created, 0))
	}
	if r.Updated > 0 {
		m.UpdateTime = timestamppb.New(time.Unix(r.Updated, 0))
	}
	return m
}

// recordFromProto maps an incoming wire message to a store record. Timestamps are
// server-set (output only), so they are ignored here. An unspecified/unknown enum maps to
// an empty string, which the store's Validate rejects.
func recordFromProto(m *memoryv1.Memory) store.Record {
	refs := make([]store.Ref, len(m.GetRefs()))
	for i, ref := range m.GetRefs() {
		refs[i] = store.Ref{Kind: refKindFromProto(ref.GetKind()), Target: ref.GetTarget()}
	}
	return store.Record{
		Name: m.GetName(), Type: typeFromProto(m.GetType()), Status: m.GetStatus(),
		Body: m.GetBody(), Refs: refs, References: m.GetReferences(),
	}
}

func typeToProto(s string) memoryv1.MemoryType {
	switch s {
	case store.TypePointer:
		return memoryv1.MemoryType_MEMORY_TYPE_POINTER
	case store.TypeDecision:
		return memoryv1.MemoryType_MEMORY_TYPE_DECISION
	case store.TypePlan:
		return memoryv1.MemoryType_MEMORY_TYPE_PLAN
	default:
		return memoryv1.MemoryType_MEMORY_TYPE_UNSPECIFIED
	}
}

func typeFromProto(t memoryv1.MemoryType) string {
	switch t {
	case memoryv1.MemoryType_MEMORY_TYPE_POINTER:
		return store.TypePointer
	case memoryv1.MemoryType_MEMORY_TYPE_DECISION:
		return store.TypeDecision
	case memoryv1.MemoryType_MEMORY_TYPE_PLAN:
		return store.TypePlan
	default:
		return ""
	}
}

func refKindToProto(s string) memoryv1.MemoryRefKind {
	switch s {
	case store.RefKindQuery:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_QUERY
	case store.RefKindNode:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE
	case store.RefKindOutput:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_OUTPUT
	case store.RefKindCommand:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_COMMAND
	case store.RefKindDoc:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_DOC
	default:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_UNSPECIFIED
	}
}

func refKindFromProto(k memoryv1.MemoryRefKind) string {
	switch k {
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_QUERY:
		return store.RefKindQuery
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE:
		return store.RefKindNode
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_OUTPUT:
		return store.RefKindOutput
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_COMMAND:
		return store.RefKindCommand
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_DOC:
		return store.RefKindDoc
	default:
		return ""
	}
}
