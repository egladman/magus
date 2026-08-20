// Package notes is the console-facing NotesService handler: a READ-ONLY view over the
// workspace's human-authored notes, both the shared store in the checkout and the private
// one on this machine.
//
// It is the deliberate opposite of the memory handler beside it. Memory is agent-written, so
// a browser edit/delete surface is its safety valve against records nobody curates. A note is
// human-written, and that guarantee IS its value: a note is the one node class the knowledge
// graph does not derive from the workspace, so nothing here corroborates it later and its
// only provenance is the person who wrote it. A browser write would put an unattributable
// author on that store and undoing it would not restore the guarantee, so this handler has no
// write path at all - the CLI's `magus notes edit` opens an editor, and that stays the way in.
//
// A note's body is UNTRUSTED in the ordinary sense that it is prose from a file: clients must
// render it as text, never as trusted HTML. The distinction from memory is about provenance,
// not about escaping.
package notes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	store "github.com/egladman/magus/internal/notes"
	notesv1 "github.com/egladman/magus/proto/gen/go/magus/notes/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/notes/v1alpha1/notesv1alpha1connect"
	"github.com/egladman/magus/types"
)

// workspace is the narrow slice of *magus.Magus the handler needs: the root that anchors a
// relative store path, and the graph that says whether an anchor still names anything.
// Satisfied structurally by *magus.Magus.
type workspace interface {
	Root() string
	KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error)
}

// Service implements notesv1alpha1connect.NotesServiceHandler over the on-disk note stores.
type Service struct {
	ws  workspace
	cfg config.Config
}

// NewService builds a NotesService handler over the workspace ws. cfg carries the two
// declared store paths; neither is guessed, because a store magus invented would be a
// location the reader never opted in to.
func NewService(ws workspace, cfg config.Config) *Service { return &Service{ws: ws, cfg: cfg} }

var _ notesv1alpha1connect.NotesServiceHandler = (*Service)(nil)

// scopedDir pairs one resolved store directory with the scope that says what putting a note
// there means.
type scopedDir struct {
	scope    store.Scope
	pbScope  notesv1.Scope
	dir      string
	declared bool
}

// stores resolves both declared locations, in the order a reader should see them. A store
// that is not declared is REPORTED as undeclared rather than omitted: "you have no notes" and
// "this workspace has nowhere to put one" call for completely different next actions, and a
// blank list says the first when it means the second.
func (s *Service) stores() []scopedDir {
	out := make([]scopedDir, 0, 2)
	add := func(declared string, scope store.Scope, pb notesv1.Scope) {
		dir, err := store.Dir(s.ws.Root(), scope, declared)
		if err != nil {
			// Undeclared (ErrDisabled) and misdeclared both land here. Both mean this store
			// contributes nothing, and the console renders that as "not set up" - the CLI is
			// where a misdeclaration gets its full diagnostic.
			out = append(out, scopedDir{scope: scope, pbScope: pb})
			return
		}
		out = append(out, scopedDir{scope: scope, pbScope: pb, dir: dir, declared: true})
	}
	add(s.cfg.Knowledge.Notes.Shared, store.ScopeShared, notesv1.Scope_SCOPE_SHARED)
	add(s.cfg.Knowledge.Notes.Private, store.ScopePrivate, notesv1.Scope_SCOPE_PRIVATE)
	return out
}

// ListNotes returns every note in both stores with its anchors already checked. Pagination is
// wired in the contract but the store returns all notes today (bounded by its own scan cap),
// so next_page_token is always empty.
//
// A graph that will not load is NOT a failure here. The structural read - what notes exist,
// what they say, where they live - stands on its own, and a console that shows nothing
// because the symbol index is cold is worse than one that shows the notes and admits it could
// not check them. Every anchor then reports UNVERIFIED, which is a distinct answer from
// "fine".
func (s *Service) ListNotes(ctx context.Context, _ *connect.Request[notesv1.ListNotesRequest]) (*connect.Response[notesv1.ListNotesResponse], error) {
	res, stale := s.resolver(ctx)
	out := &notesv1.ListNotesResponse{}
	for _, sd := range s.stores() {
		status := &notesv1.StoreStatus{Scope: sd.pbScope, Declared: sd.declared, Path: sd.dir}
		if !sd.declared {
			out.Stores = append(out.Stores, status)
			continue
		}
		found, issues, err := store.Inspect(sd.dir)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		for _, iss := range issues {
			status.Issues = append(status.Issues, issueLine(iss))
		}
		for _, n := range found {
			out.Notes = append(out.Notes, s.toProto(ctx, n, sd, res, stale, false))
		}
		status.NoteCount = int32(len(found))
		out.Stores = append(out.Stores, status)
	}
	return connect.NewResponse(out), nil
}

// splitNoteName splits a note resource name into its store and bare id. It reports false
// for anything that does not name a store, which is the whole point of folding the store
// into the name: an unprefixed name is ambiguous and must be refused, not guessed.
func splitNoteName(name string) (notesv1.Scope, string, bool) {
	store, id, found := strings.Cut(name, "/")
	if !found || id == "" {
		return notesv1.Scope_SCOPE_UNSPECIFIED, "", false
	}
	switch store {
	case "shared":
		return notesv1.Scope_SCOPE_SHARED, id, true
	case "private":
		return notesv1.Scope_SCOPE_PRIVATE, id, true
	}
	return notesv1.Scope_SCOPE_UNSPECIFIED, "", false
}

// GetNote returns one note in full, including its body. The store is carried in the name
// ("shared/x", "private/x") rather than inferred: a bare name can exist in both, and the two
// mean different things to a reader, so guessing which was meant must not happen.
func (s *Service) GetNote(ctx context.Context, req *connect.Request[notesv1.GetNoteRequest]) (*connect.Response[notesv1.Note], error) {
	want, id, ok := splitNoteName(req.Msg.GetName())
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New(`notes: name must be "shared/<note>" or "private/<note>"; the store is part of the name because a bare name can exist in both and they mean different things about who can read the note`))
	}
	for _, sd := range s.stores() {
		if sd.pbScope != want || !sd.declared {
			continue
		}
		n, err := store.Get(sd.dir, id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("notes: no %s note named %q", sd.scope, id))
			}
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		res, stale := s.resolver(ctx)
		return connect.NewResponse(s.toProto(ctx, n, sd, res, stale, true)), nil
	}
	return nil, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("notes: this workspace declares no %s notes store", scopeName(want)))
}

// resolver loads the graph once per request and returns it alongside the note-node staleness
// attrs folded onto it. Both are best effort: a cold or unbuildable graph yields a nil
// resolver, and every anchor then reports UNVERIFIED rather than a guess.
//
// Per request rather than cached, because the daemon holds a warm graph already and a stale
// answer about whether an anchor resolves is exactly the wrong thing to cache twice.
func (s *Service) resolver(ctx context.Context) (*knowledge.NoteResolver, map[string]types.KnowledgeNode) {
	g, err := s.ws.KnowledgeGraphWithSymbols(ctx)
	if err != nil || g == nil {
		return nil, nil
	}
	r := knowledge.NewNoteResolver(s.ws.Root(), g)
	byID := map[string]types.KnowledgeNode{}
	for _, n := range g.Nodes() {
		if n.Kind == types.KindNote {
			byID[n.ID] = n
		}
	}
	return &r, byID
}

// toProto renders one note. withBody is false for a listing: the prose is the largest field
// and a list of it is not what a reader is scanning for.
func (s *Service) toProto(ctx context.Context, n store.Note, sd scopedDir, res *knowledge.NoteResolver, noteNodes map[string]types.KnowledgeNode, withBody bool) *notesv1.Note {
	out := &notesv1.Note{
		Name:  n.Name,
		Scope: sd.pbScope,
		Title: n.Title,
		Tags:  n.Tags,
		Path:  s.notePath(n, sd),
	}
	if withBody {
		out.Body = n.Body
	}
	if !n.Modified.IsZero() {
		out.ModifyTime = timestamppb.New(n.Modified)
	}
	// Carried on the LISTING as well as the read, unlike the body. A client has to be able to
	// mark a capture in a list of notes: telling one apart only after opening it means a
	// reader has already taken it for authored prose by the time they learn otherwise.
	if n.Source != nil {
		src := &notesv1.Source{
			Kind: string(n.Source.Kind),
			Ref:  n.Source.Ref,
			AsOf: n.Source.AsOf,
		}
		if !n.Source.Captured.IsZero() {
			src.Captured = timestamppb.New(n.Source.Captured)
		}
		out.Source = src
	}
	scoped := res
	if res != nil {
		bound := res.ForScope(string(sd.scope))
		scoped = &bound
	}
	for _, a := range n.Anchors {
		out.Anchors = append(out.Anchors, anchorToProto(ctx, a, scoped))
	}
	out.Staleness, out.OutrunDays = staleness(noteNodes, knowledge.AnchorNodeID("note", n.Name, string(sd.scope)))
	return out
}

// notePath renders where the note lives. Workspace-relative for a shared note, absolute for a
// private one - the same distinction the scope carries, and the reason a private note is
// never staleness-annotated (the VCS index is keyed by workspace-relative path).
//
// The path comes from the note rather than from its name: a note that declares an id is
// identified by that id and NOT by where its file sits, so joining the store dir to the name
// named a file that does not exist as soon as the two diverged.
func (s *Service) notePath(n store.Note, sd scopedDir) string {
	if sd.scope != store.ScopeShared {
		return n.Path
	}
	rel, err := filepath.Rel(s.ws.Root(), n.Path)
	if err != nil {
		return n.Path
	}
	return filepath.ToSlash(rel)
}

// anchorToProto checks one anchor and renders the verdict.
//
// A nil resolver means the graph would not load, and every anchor reports UNVERIFIED. That is
// not the same as RESOLVES and must never be rendered as one: the honest report is that
// nothing was checked.
func anchorToProto(ctx context.Context, a store.Anchor, res *knowledge.NoteResolver) *notesv1.Anchor {
	out := &notesv1.Anchor{Kind: anchorKind(a.Kind), Target: a.Target}
	if res == nil {
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = "The knowledge graph would not load, so this anchor was not checked."
		return out
	}
	out.NodeId = res.NodeID(a)
	if !res.Resolves(ctx, a) {
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_DANGLING
		out.NodeId = ""
		out.Detail = "This anchor no longer names anything in the workspace. `magus notes verify` names the coarser anchor it degrades to; nothing re-points it at a guess."
		return out
	}
	// The anchor resolves, so the easy question is answered. The harder one is whether the
	// thing it points at still SAYS what the note claims - the case a reader cannot see and an
	// existence check cannot catch.
	//
	// Both empty cases are silence, not drift: no stored digest means the note was never
	// re-read against the code, and no current digest means one cannot be computed here.
	// Reporting drift from either is the false positive that trains a reader to ignore the flag.
	if a.Digest == "" {
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = "Nobody has re-read this note against the code since anchors began carrying a fingerprint, so drift cannot be detected here."
		return out
	}
	// An if rather than a switch: the two arms turn on a compound condition, not on the
	// value of one expression, so a tagged switch would have to invent a subject.
	current, derr := res.Digest(ctx, a)
	if derr != nil {
		// Distinct from RESOLVES: the anchor is live, and whether the prose still holds is
		// unknown rather than confirmed. Saying "resolves" here would report a check that
		// never ran as a check that passed.
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = "This still exists, but its fingerprint could not be computed, so nothing can tell whether the note still holds. A symbol anchor needs the symbol index: `magus graph build`."
		return out
	}
	if current == "" || current == a.Digest {
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_RESOLVES
	} else {
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_DRIFTED
		out.Detail = "This still exists, but its content changed since the note was last reviewed. Re-read the note against the code as it is now; `magus notes edit` records the new fingerprint only when a person says it still holds."
	}
	return out
}

// staleness reads the divergence the graph folded onto the note's node. An absent node or an
// absent attr is UNMEASURED, which is not "fresh" - every private note lands here today,
// because staleness is keyed by workspace-relative path and a private note's source is
// absolute.
func staleness(nodes map[string]types.KnowledgeNode, nodeID string) (notesv1.Staleness, int32) {
	n, ok := nodes[nodeID]
	if !ok {
		return notesv1.Staleness_STALENESS_UNMEASURED, 0
	}
	days := outrunDays(n.Attrs[knowledge.AttrOutrunDays])
	switch n.Attrs[knowledge.AttrStaleness] {
	case knowledge.StalenessCurrent:
		return notesv1.Staleness_STALENESS_CURRENT, 0
	case knowledge.StalenessOutrun:
		return notesv1.Staleness_STALENESS_OUTRUN, days
	case knowledge.StalenessPetrified:
		return notesv1.Staleness_STALENESS_PETRIFIED, days
	default:
		return notesv1.Staleness_STALENESS_UNMEASURED, 0
	}
}

// outrunDays parses the divergence attr into the wire's int32, clamped.
//
// The attr is graph data rather than a value this process computed, so it is parsed
// defensively: an unparseable or negative value reads as 0 (no divergence claimed), and a
// value past int32 is capped rather than wrapped into a negative day count that would
// render as prose newer than the code it describes.
func outrunDays(attr string) int32 {
	// ParseInt with bitSize 32 rather than Atoi plus a cast: the range check is the parse,
	// so an out-of-range value arrives as an error instead of a silent wrap into a
	// negative day count that would render as prose newer than the code it describes.
	days, err := strconv.ParseInt(attr, 10, 32)
	if err != nil || days <= 0 {
		return 0
	}
	return int32(days)
}

func anchorKind(k store.AnchorKind) notesv1.AnchorKind {
	switch k {
	case store.AnchorSymbol:
		return notesv1.AnchorKind_ANCHOR_KIND_SYMBOL
	case store.AnchorFile:
		return notesv1.AnchorKind_ANCHOR_KIND_FILE
	case store.AnchorProject:
		return notesv1.AnchorKind_ANCHOR_KIND_PROJECT
	case store.AnchorTarget:
		return notesv1.AnchorKind_ANCHOR_KIND_TARGET
	case store.AnchorNote:
		return notesv1.AnchorKind_ANCHOR_KIND_NOTE
	default:
		return notesv1.AnchorKind_ANCHOR_KIND_UNSPECIFIED
	}
}

func scopeName(s notesv1.Scope) string {
	if s == notesv1.Scope_SCOPE_PRIVATE {
		return knowledge.ScopePrivate
	}
	return knowledge.ScopeShared
}

// issueLine flattens one scan finding into the sentence a store card shows. The severity is
// folded into the wording rather than carried as a field: this surface reports problems, and a
// client that renders warnings differently from errors would be inviting a reader to ignore
// half of them.
func issueLine(iss store.Issue) string {
	if iss.Path == "" {
		return iss.Message
	}
	return filepath.Base(iss.Path) + ": " + iss.Message
}
