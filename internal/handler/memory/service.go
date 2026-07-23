// Package memory is the console-facing MemoryService handler: an observable, editable view
// over the durable agent-memory RECORDS the MCP magus_memory tool writes. It is a SECOND
// door onto the EXACT on-disk store that tool maintains (internal/memory), never a second
// store of its own, so the browser edit surface and the agent-facing tool share one set of
// records. Its reason to exist: an agent can accumulate stale or bloated records, and a
// human list/edit/delete surface is the safety valve.
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

	"github.com/egladman/magus/internal/memory"
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
	recs, err := memory.List(s.ws.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*memoryv1.Memory, len(recs))
	for i, r := range recs {
		out[i] = recordToProto(r)
	}
	return connect.NewResponse(&memoryv1.ListMemoriesResponse{Memories: out}), nil
}

// UpdateMemory upserts a record keyed by memory.name. allow_missing=true creates the
// record when absent (the upsert); allow_missing=false rejects an absent record with
// NotFound. An empty update_mask is a full replace, the only mode today.
func (s *Service) UpdateMemory(_ context.Context, req *connect.Request[memoryv1.UpdateMemoryRequest]) (*connect.Response[memoryv1.UpdateMemoryResponse], error) {
	root := s.ws.Root()
	rec := recordFromProto(req.Msg.GetMemory())
	if !req.Msg.GetAllowMissing() {
		if _, err := memory.Get(root, rec.Name); err != nil {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("memory: no record named "+rec.Name+" (set allow_missing to create it)"))
		}
	}
	stored, err := memory.Put(root, rec) // validates the schema
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&memoryv1.UpdateMemoryResponse{Memory: recordToProto(stored)}), nil
}

// DeleteMemory removes a record by name. allow_missing=true makes deleting an absent
// record a no-op; otherwise an absent record is NotFound.
func (s *Service) DeleteMemory(_ context.Context, req *connect.Request[memoryv1.DeleteMemoryRequest]) (*connect.Response[memoryv1.DeleteMemoryResponse], error) {
	err := memory.Delete(s.ws.Root(), req.Msg.GetName(), req.Msg.GetAllowMissing())
	if errors.Is(err, os.ErrNotExist) {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&memoryv1.DeleteMemoryResponse{}), nil
}

// GetCursor returns the cursor snapshot, empty when never written.
func (s *Service) GetCursor(_ context.Context, _ *connect.Request[memoryv1.GetCursorRequest]) (*connect.Response[memoryv1.GetCursorResponse], error) {
	content, err := memory.ReadCursor(s.ws.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.GetCursorResponse{Content: content}), nil
}

// UpdateCursor overwrites the cursor snapshot.
func (s *Service) UpdateCursor(_ context.Context, req *connect.Request[memoryv1.UpdateCursorRequest]) (*connect.Response[memoryv1.UpdateCursorResponse], error) {
	content := req.Msg.GetContent()
	if err := memory.WriteCursor(s.ws.Root(), content); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&memoryv1.UpdateCursorResponse{Content: content}), nil
}

// recordToProto maps a stored record to the wire message, stamping the output-only
// timestamps from the store's unix seconds.
func recordToProto(r memory.Record) *memoryv1.Memory {
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
func recordFromProto(m *memoryv1.Memory) memory.Record {
	refs := make([]memory.Ref, len(m.GetRefs()))
	for i, ref := range m.GetRefs() {
		refs[i] = memory.Ref{Kind: refKindFromProto(ref.GetKind()), Target: ref.GetTarget()}
	}
	return memory.Record{
		Name: m.GetName(), Type: typeFromProto(m.GetType()), Status: m.GetStatus(),
		Body: m.GetBody(), Refs: refs, References: m.GetReferences(),
	}
}

func typeToProto(s string) memoryv1.MemoryType {
	switch s {
	case memory.TypePointer:
		return memoryv1.MemoryType_MEMORY_TYPE_POINTER
	case memory.TypeDecision:
		return memoryv1.MemoryType_MEMORY_TYPE_DECISION
	case memory.TypePlan:
		return memoryv1.MemoryType_MEMORY_TYPE_PLAN
	default:
		return memoryv1.MemoryType_MEMORY_TYPE_UNSPECIFIED
	}
}

func typeFromProto(t memoryv1.MemoryType) string {
	switch t {
	case memoryv1.MemoryType_MEMORY_TYPE_POINTER:
		return memory.TypePointer
	case memoryv1.MemoryType_MEMORY_TYPE_DECISION:
		return memory.TypeDecision
	case memoryv1.MemoryType_MEMORY_TYPE_PLAN:
		return memory.TypePlan
	default:
		return ""
	}
}

func refKindToProto(s string) memoryv1.MemoryRefKind {
	switch s {
	case memory.RefKindQuery:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_QUERY
	case memory.RefKindNode:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE
	case memory.RefKindOutput:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_OUTPUT
	case memory.RefKindCommand:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_COMMAND
	case memory.RefKindDoc:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_DOC
	default:
		return memoryv1.MemoryRefKind_MEMORY_REF_KIND_UNSPECIFIED
	}
}

func refKindFromProto(k memoryv1.MemoryRefKind) string {
	switch k {
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_QUERY:
		return memory.RefKindQuery
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_NODE:
		return memory.RefKindNode
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_OUTPUT:
		return memory.RefKindOutput
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_COMMAND:
		return memory.RefKindCommand
	case memoryv1.MemoryRefKind_MEMORY_REF_KIND_DOC:
		return memory.RefKindDoc
	default:
		return ""
	}
}
