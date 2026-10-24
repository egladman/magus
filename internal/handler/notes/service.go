// Package notes is the console-facing NotesService handler: a READ-ONLY view over the
// workspace's human-authored notes, both the shared store in the checkout and the private
// one on this machine.
//
// It is the deliberate opposite of the memory handler beside it. Memory is agent-written, so
// a browser edit/delete route is its safety valve against records nobody curates. A note is
// human-written, and that guarantee IS its value: a note is the one node class the knowledge
// graph does not derive from the workspace, so nothing here corroborates it later and its
// only provenance is the person who wrote it. A browser write would put an unattributable
// author on that store and undoing it would not restore the guarantee, so this handler has no
// write path at all: the CLI's `magus notes edit` opens an editor, and that stays the way in.
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
	KnowledgeGraph(ctx context.Context, req types.GraphRequest) (*knowledge.Graph, error)
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
			// contributes nothing, and the console renders that as "not set up"; the CLI is
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
// A graph that will not load is NOT a failure here. The structural read (what notes exist,
// what they say, where they live) stands on its own, and a console that shows nothing
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
		graded, err := grade(ctx, sd, res)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		for _, n := range found {
			pn, err := s.toProto(n, sd, res, graded, stale, false)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnimplemented, err)
			}
			out.Notes = append(out.Notes, pn)
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
			errors.New(`notes: name must be "shared/<note>" or "private/<note>", the store is part of the name because a bare name can exist in both and they mean different things about who can read the note`))
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
		graded, err := grade(ctx, sd, res)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		pn, err := s.toProto(n, sd, res, graded, stale, true)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnimplemented, err)
		}
		return connect.NewResponse(pn), nil
	}
	return nil, connect.NewError(connect.CodeNotFound,
		fmt.Errorf("notes: this workspace declares no %s notes store", scopeName(want)))
}

// resolver loads the graph once per request and returns it alongside the note-node staleness
// attrs folded onto it. Both are best effort: a cold or unbuildable graph yields a nil
// resolver, and every anchor then reports UNVERIFIED rather than a guess.
//
// Per request rather than cached, because the server holds a warm graph already and a stale
// answer about whether an anchor resolves is exactly the wrong thing to cache twice.
func (s *Service) resolver(ctx context.Context) (*knowledge.NoteResolver, map[string]types.KnowledgeNode) {
	g, err := s.ws.KnowledgeGraph(ctx, types.GraphRequest{Symbols: types.AllSymbols})
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
func (s *Service) toProto(n store.Note, sd scopedDir, res *knowledge.NoteResolver, graded map[anchorKey]store.IssueCode, noteNodes map[string]types.KnowledgeNode, withBody bool) (*notesv1.Note, error) {
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
	for i, a := range n.Anchors {
		// Absent only when the note changed between the store walk and this one; nothing graded
		// what is here now.
		status, ok := graded[anchorKey{note: n.Name, pos: i}]
		if !ok {
			status = store.StatusUngraded
		}
		pa, err := anchorToProto(a, status, scoped)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Name, err)
		}
		out.Anchors = append(out.Anchors, pa)
	}
	out.Staleness, out.OutrunDays = staleness(noteNodes, knowledge.AnchorNodeID("note", n.Name, string(sd.scope)))
	return out, nil
}

// notePath renders where the note lives. Workspace-relative for a shared note, absolute for a
// private one, the same distinction the scope carries, and the reason a private note is
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

// anchorKey names one anchor the way notes.ResolveAnchors does: its note, and its index there.
type anchorKey struct {
	note string
	pos  int
}

// grade runs `magus notes verify`'s grader over one store. A nil resolver means the graph
// would not load, and every anchor grades ungraded.
func grade(ctx context.Context, sd scopedDir, res *knowledge.NoteResolver) (map[anchorKey]store.IssueCode, error) {
	var scoped store.Resolver
	if res != nil {
		scoped = res.ForScope(string(sd.scope))
	}
	all, err := store.ResolveAnchors(ctx, sd.dir, scoped)
	if err != nil {
		return nil, err
	}
	out := make(map[anchorKey]store.IssueCode, len(all))
	for _, r := range all {
		out[anchorKey{note: r.Note, pos: r.Pos}] = r.Status
	}
	return out, nil
}

// anchorToProto renders the grade notes gave one anchor. It maps and never re-grades, so the
// console cannot disagree with `magus notes verify`.
//
// Ungraded is not the same as RESOLVES and must never be rendered as one: the honest report is
// that nothing was checked.
func anchorToProto(a store.Anchor, status store.IssueCode, res *knowledge.NoteResolver) (*notesv1.Anchor, error) {
	kind, err := anchorKind(a.Kind)
	if err != nil {
		return nil, err
	}
	out := &notesv1.Anchor{Kind: kind, Target: a.Target}
	if res != nil {
		out.NodeId = res.NodeID(a)
	}
	switch status {
	case "":
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_RESOLVES
	case store.CodeDanglingAnchor:
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_DANGLING
		out.NodeId = ""
		out.Detail = "This anchor no longer names anything in the workspace. `magus notes verify` names the coarser anchor it degrades to; nothing re-points it at a guess."
	case store.CodeDriftedAnchor:
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_DRIFTED
		out.Detail = "This still exists, but what it is changed since the note was last reviewed: a symbol's signature, or a file's content. Re-read the note against the code as it is now; `magus notes edit` records it again only when a person says it still holds."
	case store.CodeUnverifiableAnchor:
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = "This still exists, but nothing can tell whether the note still holds: a symbol note recorded no signature, or none could be read here. `magus notes verify` says which."
	case store.StatusUngraded:
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = "The knowledge graph would not load, so this anchor was not checked."
	default:
		out.Status = notesv1.AnchorStatus_ANCHOR_STATUS_UNVERIFIED
		out.Detail = fmt.Sprintf("notes graded this anchor %q, which this service does not render.", status)
	}
	return out, nil
}

// staleness reads the divergence the graph folded onto the note's node. An absent node or an
// absent attr is UNMEASURED, which is not "fresh"; every private note lands here today,
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
// defensively: an unparsable or negative value reads as 0 (no divergence claimed), and a
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

// errAnchorKindOffAPI marks an anchor kind the notes proto cannot carry yet, so a note holding
// one fails loudly instead of rendering as some other kind.
var errAnchorKindOffAPI = errors.New("not on the notes API yet")

func anchorKind(k store.AnchorKind) (notesv1.AnchorKind, error) {
	switch k {
	case store.AnchorSymbol:
		return notesv1.AnchorKind_ANCHOR_KIND_SYMBOL, nil
	case store.AnchorFile:
		return notesv1.AnchorKind_ANCHOR_KIND_FILE, nil
	case store.AnchorProject:
		return notesv1.AnchorKind_ANCHOR_KIND_PROJECT, nil
	case store.AnchorTarget:
		return notesv1.AnchorKind_ANCHOR_KIND_TARGET, nil
	case store.AnchorNote:
		return notesv1.AnchorKind_ANCHOR_KIND_NOTE, nil
	case store.AnchorDiagnostic:
		return notesv1.AnchorKind_ANCHOR_KIND_UNSPECIFIED, fmt.Errorf("notes: anchor kind %s: %w", k, errAnchorKindOffAPI)
	default:
		return notesv1.AnchorKind_ANCHOR_KIND_UNSPECIFIED, nil
	}
}

func scopeName(s notesv1.Scope) string {
	if s == notesv1.Scope_SCOPE_PRIVATE {
		return knowledge.ScopePrivate
	}
	return knowledge.ScopeShared
}

// issueLine flattens one scan finding into the sentence a store card shows. The severity is
// folded into the wording rather than carried as a field: this service reports problems, and a
// client that renders warnings differently from errors would be inviting a reader to ignore
// half of them.
func issueLine(iss store.Issue) string {
	if iss.Path == "" {
		return iss.Message
	}
	return fmt.Sprintf("%s: %s", filepath.Base(iss.Path), iss.Message)
}
