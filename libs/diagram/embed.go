package diagram

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/result"
)

// Source is the diagram module's Buzz source, served as `import "magus/diagram"` by a host
// with no checkout on disk.
//
//go:embed diagram.buzz
var Source string

// DeclareModules makes sess resolve "magus/diagram" and the source modules it
// imports. The host still provides "magus" and the stdlib.
func DeclareModules(sess *buzz.Session) {
	sess.SetModuleDecls("magus/diagram", Source)
	sess.SetModuleDecls(result.ModulePath, result.Source)
}

// SourceSHA256 is the hex SHA-256 of Source: the identity of the diagram engine this binary
// renders with, which moves only when diagram.buzz does.
func SourceSHA256() string {
	sum := sha256.Sum256([]byte(Source))
	return hex.EncodeToString(sum[:])
}

// The types below mirror the module's records field for field, under the Buzz field names;
// TestMirrorMatchesTheModule holds them to the declarations.

// Look, Stroke, Direction, Axis, Level and ActorKind each hold the name of one of the
// module's enum cases, such as Look("plain") or Level("package").
type (
	Look      string
	Stroke    string
	Direction string
	Axis      string
	Level     string
	ActorKind string
)

// Diagram mirrors diagram's Diagram. An empty Direction is Direction("across"); a nil Level
// lets the boxes decide it.
type Diagram struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Eyebrow     string        `json:"eyebrow"`
	Desc        string        `json:"desc"`
	Direction   Direction     `json:"direction"`
	Level       *Level        `json:"level"`
	UnscopedWhy string        `json:"unscoped_why"`
	GraphEdges  bool          `json:"graph_edges"`
	Boxes       []Box         `json:"boxes"`
	Scopes      [][]Dir       `json:"scopes"`
	Exclusions  []Exclusion   `json:"exclusions"`
	HiddenEdges []HiddenEdges `json:"hidden_edges"`
	EdgeMarks   []EdgeMark    `json:"edge_marks"`
	Flows       []Flow        `json:"flows"`
	Zones       []Zone        `json:"zones"`
	Alignments  []Alignment   `json:"alignments"`
	Legends     []Legend      `json:"legends"`
}

// Box mirrors diagram's Box: exactly one of Dir, Group and Actor is set. A host has no
// magus\refs result to anchor a symbol with, so Box carries no symbol.
type Box struct {
	Dir   *Dir   `json:"dir"`
	Group *[]Dir `json:"group"`
	Actor *Actor `json:"actor"`
	Label string `json:"label"`
	Sub   string `json:"sub"`
	Tag   string `json:"tag"`
	Focal bool   `json:"focal"`
	Look  *Look  `json:"look"`
}

// Actor mirrors diagram's Actor. A nil Kind is ActorKind("step"): a host draws its own
// units, a project, a target or a job, as the steps of a Level.process diagram. A nil Look
// follows the kind. A host holds no magus\refs result, so it builds no stage.
type Actor struct {
	Name string     `json:"name"`
	Sub  string     `json:"sub"`
	Tag  string     `json:"tag"`
	Link string     `json:"link"`
	Look *Look      `json:"look"`
	Kind *ActorKind `json:"kind"`
	Dir  *Dir       `json:"dir"`
}

// Exclusion mirrors diagram's Exclusion.
type Exclusion struct {
	Dirs []Dir  `json:"dirs"`
	Why  string `json:"why"`
}

// HiddenEdges mirrors diagram's HiddenEdges.
type HiddenEdges struct {
	Src []Dir  `json:"src"`
	Dst []Dir  `json:"dst"`
	Why string `json:"why"`
}

// EdgeMark mirrors diagram's EdgeMark.
type EdgeMark struct {
	Src    Dir     `json:"src"`
	Dst    Dir     `json:"dst"`
	Label  string  `json:"label"`
	Stroke *Stroke `json:"stroke"`
}

// End mirrors diagram's End: exactly one of Dir and Actor is set.
type End struct {
	Dir   *Dir   `json:"dir"`
	Actor *Actor `json:"actor"`
}

// Flow mirrors diagram's Flow.
type Flow struct {
	Src    End     `json:"src"`
	Dst    End     `json:"dst"`
	Label  string  `json:"label"`
	Stroke *Stroke `json:"stroke"`
}

// Zone mirrors diagram's Zone.
type Zone struct {
	Label    string  `json:"label"`
	Dirs     []Dir   `json:"dirs"`
	Actors   []Actor `json:"actors"`
	Boundary bool    `json:"boundary"`
}

// Alignment mirrors diagram's Alignment.
type Alignment struct {
	Axis   Axis    `json:"axis"`
	Dirs   []Dir   `json:"dirs"`
	Actors []Actor `json:"actors"`
}

// Legend mirrors diagram's Legend.
type Legend struct {
	Look  Look   `json:"look"`
	Label string `json:"label"`
}

// Dir mirrors the magus\Dir record diagram reads, under its Buzz field names; types.Dir is
// the same record under its JSON ones.
type Dir struct {
	Path           string    `json:"path"`
	ID             string    `json:"id"`
	Layer          string    `json:"layer"`
	Language       string    `json:"language"`
	Imports        []string  `json:"imports"`
	ImportedBy     []string  `json:"imported_by"`
	ImportsIndexed bool      `json:"imports_indexed"`
	Calls          []DirCall `json:"calls"`
	CalledBy       []DirCall `json:"called_by"`
	Children       []string  `json:"children"`
	Files          int       `json:"files"`
}

// DirCall mirrors magus\DirCall.
type DirCall struct {
	Dir       string `json:"dir"`
	Transport string `json:"transport"`
	Marker    string `json:"marker"`
	Source    string `json:"source"`
}

// Findings is the module refusing a diagram: every finding, one per line, each naming the
// call to change. A case name no enum holds is refused the same way.
type Findings struct {
	Text string
}

func (f *Findings) Error() string { return "diagram: " + f.Text }

// drawEntry is constant: data reaches it as call arguments, never as source. A host
// cannot build a Buzz enum case, so it sends case names and decoded walks each enum's
// cases to rebuild them. draw catches findings so a host tells a refusal from a fault.
const drawEntry = `import "magus/diagram";

export fun draw(f: diagram\Diagram, anchorHref: str) > [str] {
    try {
        final rendered = diagram\draw(decoded(f), theme: diagram\Theme.page, anchorHref: anchorHref);
        if (rendered.error -> e) {
            return ["", e.message];
        }
        return [rendered.value!, ""];
    } catch (findings: str) {
        return ["", findings];
    }
    return ["", ""];
}

fun caseName(v: any) > str? {
    if (v == null) { return null; }
    final s: str = v;
    return s;
}

fun refused(at: str, name: str, kind: str) > str {
    return "{at} is \"{name}\", which names no {kind} case";
}

fun look(v: any, at: str) > diagram\Look? !> str {
    final s = caseName(v);
    if (s == null) { return null; }
    foreach (c in diagram\Look) {
        if (c.value == s!) { return c; }
    }
    throw refused(at, name: s!, kind: "Look");
}

fun stroke(v: any, at: str) > diagram\Stroke? !> str {
    final s = caseName(v);
    if (s == null) { return null; }
    foreach (c in diagram\Stroke) {
        if (c.value == s!) { return c; }
    }
    throw refused(at, name: s!, kind: "Stroke");
}

fun direction(v: any, at: str) > diagram\Direction !> str {
    var s = caseName(v) ?? "";
    if (s == "") { s = "across"; }
    foreach (c in diagram\Direction) {
        if (c.value == s) { return c; }
    }
    throw refused(at, name: s, kind: "Direction");
}

fun level(v: any, at: str) > diagram\Level? !> str {
    final s = caseName(v);
    if (s == null) { return null; }
    foreach (c in diagram\Level) {
        if (c.value == s!) { return c; }
    }
    throw refused(at, name: s!, kind: "Level");
}

fun actorKind(v: any, at: str) > diagram\ActorKind !> str {
    final s = caseName(v) ?? "step";
    foreach (c in diagram\ActorKind) {
        if (c.value == s) { return c; }
    }
    throw refused(at, name: s, kind: "ActorKind");
}

fun axis(v: any, at: str) > diagram\Axis !> str {
    final s = caseName(v) ?? "";
    foreach (c in diagram\Axis) {
        if (c.value == s) { return c; }
    }
    throw refused(at, name: s, kind: "Axis");
}

fun actor(a: diagram\Actor?, at: str) > diagram\Actor? !> str {
    if (a == null) { return null; }
    final x = a!;
    return diagram\Actor{
        name = x.name, sub = x.sub, tag = x.tag, link = x.link, look = look(x.look, at: at + ".look"),
        kind = actorKind(x.kind, at: at + ".kind"), dir = x.dir,
    };
}

fun actors(list: [diagram\Actor], at: str) > [diagram\Actor] !> str {
    final kept: mut [diagram\Actor] = mut [<diagram\Actor>];
    var i = 0;
    while (i < list.len()) {
        kept.append(actor(list[i], at: "{at}[{i}]")!);
        i = i + 1;
    }
    return kept;
}

fun decoded(f: diagram\Diagram) > diagram\Diagram !> str {
    final at = "diagram \"{f.id}\": ";
    final boxes: mut [diagram\Box] = mut [<diagram\Box>];
    var i = 0;
    while (i < f.boxes.len()) {
        final b = f.boxes[i];
        boxes.append(diagram\Box{
            dir = b.dir, group = b.group, actor = actor(b.actor, at: "{at}boxes[{i}].actor"),
            label = b.label, sub = b.sub, tag = b.tag, focal = b.focal,
            look = look(b.look, at: "{at}boxes[{i}].look"), symbol = b.symbol,
        });
        i = i + 1;
    }
    final marks: mut [diagram\EdgeMark] = mut [<diagram\EdgeMark>];
    i = 0;
    while (i < f.edgeMarks.len()) {
        final m = f.edgeMarks[i];
        marks.append(diagram\EdgeMark{ src = m.src, dst = m.dst, label = m.label, stroke = stroke(m.stroke, at: "{at}edge_marks[{i}].stroke") });
        i = i + 1;
    }
    final flows: mut [diagram\Flow] = mut [<diagram\Flow>];
    i = 0;
    while (i < f.flows.len()) {
        final fl = f.flows[i];
        flows.append(diagram\Flow{
            src = diagram\End{ dir = fl.src.dir, actor = actor(fl.src.actor, at: "{at}flows[{i}].src.actor") },
            dst = diagram\End{ dir = fl.dst.dir, actor = actor(fl.dst.actor, at: "{at}flows[{i}].dst.actor") },
            label = fl.label, stroke = stroke(fl.stroke, at: "{at}flows[{i}].stroke"),
        });
        i = i + 1;
    }
    final zones: mut [diagram\Zone] = mut [<diagram\Zone>];
    i = 0;
    while (i < f.zones.len()) {
        final z = f.zones[i];
        zones.append(diagram\Zone{ label = z.label, dirs = z.dirs, actors = actors(z.actors, at: "{at}zones[{i}].actors"), boundary = z.boundary });
        i = i + 1;
    }
    final alignments: mut [diagram\Alignment] = mut [<diagram\Alignment>];
    i = 0;
    while (i < f.alignments.len()) {
        final a = f.alignments[i];
        alignments.append(diagram\Alignment{
            axis = axis(a.axis, at: "{at}alignments[{i}].axis"), dirs = a.dirs,
            actors = actors(a.actors, at: "{at}alignments[{i}].actors"),
        });
        i = i + 1;
    }
    final legends: mut [diagram\Legend] = mut [<diagram\Legend>];
    i = 0;
    while (i < f.legends.len()) {
        final l = f.legends[i];
        final named = look(l.look, at: "{at}legends[{i}].look");
        if (named == null) { throw "{at}legends[{i}].look is missing; name a Look case"; }
        legends.append(diagram\Legend{ look = named!, label = l.label });
        i = i + 1;
    }
    return diagram\Diagram{
        id = f.id, title = f.title, eyebrow = f.eyebrow, desc = f.desc,
        direction = direction(f.direction, at: "{at}direction"), level = level(f.level, at: "{at}level"),
        unscopedWhy = f.unscopedWhy, graphEdges = f.graphEdges,
        boxes = boxes, scopes = f.scopes, exclusions = f.exclusions, hiddenEdges = f.hiddenEdges,
        edgeMarks = marks, flows = flows, zones = zones, alignments = alignments, legends = legends,
    };
}
`

// Draw lays fig out with diagram\draw and paints it with Theme.page, the palette that
// follows a page's CSS variables. anchorHref is diagram's link template, with {path}.
//
// sess must hold DeclareModules' modules and "magus", for its record types.
// Draw executes its entry program in sess, so a caller gives it a session of its own. A
// diagram the module refuses, or a case name no enum holds, is a *Findings.
func Draw(ctx context.Context, sess *buzz.Session, fig Diagram, anchorHref string) (string, error) {
	if err := sess.Exec(ctx, drawEntry); err != nil {
		return "", fmt.Errorf("diagram: load the draw entry: %w", err)
	}
	fn, ok := sess.Exports()["draw"]
	if !ok {
		return "", errors.New("diagram: the draw entry exported no draw")
	}
	v, err := sess.CallValue(ctx, fn, []vm.Value{fig.value(), vm.StringValue(anchorHref)})
	if err != nil {
		return "", fmt.Errorf("diagram: draw %s: %w", fig.ID, err)
	}
	if !v.IsList() || len(v.ListItems()) != 2 {
		return "", fmt.Errorf("diagram: draw %s returned %s", fig.ID, v.Kind())
	}
	items := v.ListItems()
	if findings := items[1].AsString(); findings != "" {
		return "", &Findings{Text: findings}
	}
	return items[0].AsString(), nil
}

// xmlDeclaration is XmlDeclaration in diagram.buzz.
const xmlDeclaration = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"

// Standalone is svg as a file of its own. Draw returns the inline form.
func Standalone(svg string) string {
	return xmlDeclaration + svg
}

// field is one key of a record as a host hands it to Buzz: a map read through the record's
// declared type.
type field struct {
	key string
	val vm.Value
}

func record(fields []field) vm.Value {
	m := vm.NewMap()
	for _, f := range fields {
		m.MapSet(f.key, f.val)
	}
	return m
}

func (f Diagram) value() vm.Value {
	return record([]field{
		{"id", vm.StringValue(f.ID)},
		{"title", vm.StringValue(f.Title)},
		{"eyebrow", vm.StringValue(f.Eyebrow)},
		{"desc", vm.StringValue(f.Desc)},
		{"direction", vm.StringValue(string(f.Direction))},
		{"level", optional(f.Level, caseValue[Level])},
		{"unscopedWhy", vm.StringValue(f.UnscopedWhy)},
		{"graphEdges", vm.BoolValue(f.GraphEdges)},
		{"boxes", list(f.Boxes, Box.value)},
		{"scopes", list(f.Scopes, dirs)},
		{"exclusions", list(f.Exclusions, Exclusion.value)},
		{"hiddenEdges", list(f.HiddenEdges, HiddenEdges.value)},
		{"edgeMarks", list(f.EdgeMarks, EdgeMark.value)},
		{"flows", list(f.Flows, Flow.value)},
		{"zones", list(f.Zones, Zone.value)},
		{"alignments", list(f.Alignments, Alignment.value)},
		{"legends", list(f.Legends, Legend.value)},
	})
}

func (b Box) value() vm.Value {
	return record([]field{
		{"dir", optional(b.Dir, Dir.value)},
		{"group", optional(b.Group, dirs)},
		{"actor", optional(b.Actor, Actor.value)},
		{"label", vm.StringValue(b.Label)},
		{"sub", vm.StringValue(b.Sub)},
		{"tag", vm.StringValue(b.Tag)},
		{"focal", vm.BoolValue(b.Focal)},
		{"look", optional(b.Look, caseValue[Look])},
		{"symbol", vm.Null},
	})
}

func dirs(ds []Dir) vm.Value { return list(ds, Dir.value) }

func (a Actor) value() vm.Value {
	return record([]field{
		{"name", vm.StringValue(a.Name)},
		{"sub", vm.StringValue(a.Sub)},
		{"tag", vm.StringValue(a.Tag)},
		{"link", vm.StringValue(a.Link)},
		{"look", optional(a.Look, caseValue[Look])},
		{"kind", optional(a.Kind, caseValue[ActorKind])},
		{"dir", optional(a.Dir, Dir.value)},
		{"refs", vm.Null},
	})
}

func (e Exclusion) value() vm.Value {
	return record([]field{{"dirs", dirs(e.Dirs)}, {"why", vm.StringValue(e.Why)}})
}

func (h HiddenEdges) value() vm.Value {
	return record([]field{{"src", dirs(h.Src)}, {"dst", dirs(h.Dst)}, {"why", vm.StringValue(h.Why)}})
}

func (m EdgeMark) value() vm.Value {
	return record([]field{
		{"src", m.Src.value()},
		{"dst", m.Dst.value()},
		{"label", vm.StringValue(m.Label)},
		{"stroke", optional(m.Stroke, caseValue[Stroke])},
	})
}

func (e End) value() vm.Value {
	return record([]field{{"dir", optional(e.Dir, Dir.value)}, {"actor", optional(e.Actor, Actor.value)}})
}

func (f Flow) value() vm.Value {
	return record([]field{
		{"src", f.Src.value()},
		{"dst", f.Dst.value()},
		{"label", vm.StringValue(f.Label)},
		{"stroke", optional(f.Stroke, caseValue[Stroke])},
	})
}

func (z Zone) value() vm.Value {
	return record([]field{
		{"label", vm.StringValue(z.Label)},
		{"dirs", dirs(z.Dirs)},
		{"actors", list(z.Actors, Actor.value)},
		{"boundary", vm.BoolValue(z.Boundary)},
	})
}

func (a Alignment) value() vm.Value {
	return record([]field{
		{"axis", vm.StringValue(string(a.Axis))},
		{"dirs", dirs(a.Dirs)},
		{"actors", list(a.Actors, Actor.value)},
	})
}

func (l Legend) value() vm.Value {
	return record([]field{{"look", vm.StringValue(string(l.Look))}, {"label", vm.StringValue(l.Label)}})
}

func (d Dir) value() vm.Value {
	return record([]field{
		{"path", vm.StringValue(d.Path)},
		{"id", vm.StringValue(d.ID)},
		{"layer", vm.StringValue(d.Layer)},
		{"language", vm.StringValue(d.Language)},
		{"imports", list(d.Imports, vm.StringValue)},
		{"importedBy", list(d.ImportedBy, vm.StringValue)},
		{"importsIndexed", vm.BoolValue(d.ImportsIndexed)},
		{"calls", list(d.Calls, DirCall.value)},
		{"calledBy", list(d.CalledBy, DirCall.value)},
		{"children", list(d.Children, vm.StringValue)},
		{"files", vm.IntValue(int64(d.Files))},
	})
}

func (c DirCall) value() vm.Value {
	return record([]field{
		{"dir", vm.StringValue(c.Dir)},
		{"transport", vm.StringValue(c.Transport)},
		{"marker", vm.StringValue(c.Marker)},
		{"source", vm.StringValue(c.Source)},
	})
}

// caseValue is an enum case's name as the str the entry decodes.
func caseValue[T ~string](c T) vm.Value { return vm.StringValue(string(c)) }

// list is never null: diagram iterates every list field a host hands it.
func list[T any](items []T, value func(T) vm.Value) vm.Value {
	out := make([]vm.Value, len(items))
	for i, it := range items {
		out[i] = value(it)
	}
	return vm.ListValue(out)
}

func optional[T any](p *T, value func(T) vm.Value) vm.Value {
	if p == nil {
		return vm.Null
	}
	return value(*p)
}
