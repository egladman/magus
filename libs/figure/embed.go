package figure

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Source is the figure module's Buzz source, served as `import "magus/figure"` by a host
// with no checkout on disk.
//
//go:embed figure.buzz
var Source string

// The types below mirror the module's records field for field, under the Buzz field names;
// TestMirrorMatchesTheModule holds them to the declarations.

// Look, Stroke, Direction and Axis each hold the name of one of the module's enum cases,
// such as Look("plain") or Direction("down").
type (
	Look      string
	Stroke    string
	Direction string
	Axis      string
)

// Figure mirrors figure's Figure. An empty Direction is Direction("across").
type Figure struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Eyebrow     string        `json:"eyebrow"`
	Desc        string        `json:"desc"`
	Direction   Direction     `json:"direction"`
	Generated   bool          `json:"generated"`
	UnscopedWhy string        `json:"unscopedWhy"`
	GraphEdges  bool          `json:"graphEdges"`
	Boxes       []Box         `json:"boxes"`
	Scopes      []DirSet      `json:"scopes"`
	Exclusions  []Exclusion   `json:"exclusions"`
	HiddenEdges []HiddenEdges `json:"hiddenEdges"`
	EdgeMarks   []EdgeMark    `json:"edgeMarks"`
	Flows       []Flow        `json:"flows"`
	Zones       []Zone        `json:"zones"`
	Alignments  []Alignment   `json:"alignments"`
	Legends     []Legend      `json:"legends"`
}

// Box mirrors figure's Box: exactly one of Dir, Group and Actor is set. A host has no
// magus\refs result to anchor a symbol with, so Box carries no symbol.
type Box struct {
	Dir   *Dir    `json:"dir"`
	Group *DirSet `json:"group"`
	Actor *Actor  `json:"actor"`
	Label string  `json:"label"`
	Sub   string  `json:"sub"`
	Tag   string  `json:"tag"`
	Focal bool    `json:"focal"`
	Look  *Look   `json:"look"`
}

// DirSet mirrors figure's DirSet. Named is how the set was built, for findings.
type DirSet struct {
	Dirs  []Dir  `json:"dirs"`
	Named string `json:"named"`
}

// Actor mirrors figure's Actor. A nil Look is Look("external").
type Actor struct {
	Name string `json:"name"`
	Sub  string `json:"sub"`
	Tag  string `json:"tag"`
	Link string `json:"link"`
	Look *Look  `json:"look"`
}

// Exclusion mirrors figure's Exclusion.
type Exclusion struct {
	Set DirSet `json:"set"`
	Why string `json:"why"`
}

// HiddenEdges mirrors figure's HiddenEdges.
type HiddenEdges struct {
	Src DirSet `json:"src"`
	Dst DirSet `json:"dst"`
	Why string `json:"why"`
}

// EdgeMark mirrors figure's EdgeMark.
type EdgeMark struct {
	Src    Dir     `json:"src"`
	Dst    Dir     `json:"dst"`
	Label  string  `json:"label"`
	Stroke *Stroke `json:"stroke"`
}

// End mirrors figure's End: exactly one of Dir and Actor is set.
type End struct {
	Dir   *Dir   `json:"dir"`
	Actor *Actor `json:"actor"`
}

// Flow mirrors figure's Flow.
type Flow struct {
	Src    End     `json:"src"`
	Dst    End     `json:"dst"`
	Label  string  `json:"label"`
	Stroke *Stroke `json:"stroke"`
}

// Zone mirrors figure's Zone.
type Zone struct {
	Label    string  `json:"label"`
	Dirs     *DirSet `json:"dirs"`
	Actors   []Actor `json:"actors"`
	Boundary bool    `json:"boundary"`
}

// Alignment mirrors figure's Alignment.
type Alignment struct {
	Axis   Axis    `json:"axis"`
	Dirs   *DirSet `json:"dirs"`
	Actors []Actor `json:"actors"`
}

// Legend mirrors figure's Legend.
type Legend struct {
	Look  Look   `json:"look"`
	Label string `json:"label"`
}

// Dir mirrors the magus\Dir record figure reads, under its Buzz field names; types.Dir is
// the same record under its JSON ones.
type Dir struct {
	Path           string    `json:"path"`
	ID             string    `json:"id"`
	Layer          string    `json:"layer"`
	Language       string    `json:"language"`
	Imports        []string  `json:"imports"`
	ImportedBy     []string  `json:"importedBy"`
	ImportsIndexed bool      `json:"importsIndexed"`
	Calls          []DirCall `json:"calls"`
	CalledBy       []DirCall `json:"calledBy"`
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

// Findings is the module refusing a figure: every finding, one per line, each naming the
// call to change. A case name no enum holds is refused the same way.
type Findings struct {
	Text string
}

func (f *Findings) Error() string { return "figure: " + f.Text }

// drawEntry is constant: data reaches it as call arguments, never as source. A host
// cannot build a Buzz enum case, so it sends case names and decoded walks each enum's
// cases to rebuild them. draw catches findings so a host tells a refusal from a fault.
const drawEntry = `import "magus/figure";

export fun draw(f: figure\Figure, anchorHref: str) > [str] {
    try {
        return [figure\draw(decoded(f), theme: figure\Theme.page, anchorHref: anchorHref), ""];
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

fun look(v: any, at: str) > figure\Look? !> str {
    final s = caseName(v);
    if (s == null) { return null; }
    foreach (c in figure\Look) {
        if (c.value == s!) { return c; }
    }
    throw refused(at, name: s!, kind: "Look");
}

fun stroke(v: any, at: str) > figure\Stroke? !> str {
    final s = caseName(v);
    if (s == null) { return null; }
    foreach (c in figure\Stroke) {
        if (c.value == s!) { return c; }
    }
    throw refused(at, name: s!, kind: "Stroke");
}

fun direction(v: any, at: str) > figure\Direction !> str {
    var s = caseName(v) ?? "";
    if (s == "") { s = "across"; }
    foreach (c in figure\Direction) {
        if (c.value == s) { return c; }
    }
    throw refused(at, name: s, kind: "Direction");
}

fun axis(v: any, at: str) > figure\Axis !> str {
    final s = caseName(v) ?? "";
    foreach (c in figure\Axis) {
        if (c.value == s) { return c; }
    }
    throw refused(at, name: s, kind: "Axis");
}

fun actor(a: figure\Actor?, at: str) > figure\Actor? !> str {
    if (a == null) { return null; }
    final x = a!;
    return figure\Actor{ name = x.name, sub = x.sub, tag = x.tag, link = x.link, look = look(x.look, at: at + ".look") };
}

fun actors(list: [figure\Actor], at: str) > [figure\Actor] !> str {
    final kept: mut [figure\Actor] = mut [<figure\Actor>];
    var i = 0;
    while (i < list.len()) {
        kept.append(actor(list[i], at: "{at}[{i}]")!);
        i = i + 1;
    }
    return kept;
}

fun decoded(f: figure\Figure) > figure\Figure !> str {
    final at = "figure \"{f.id}\": ";
    final boxes: mut [figure\Box] = mut [<figure\Box>];
    var i = 0;
    while (i < f.boxes.len()) {
        final b = f.boxes[i];
        boxes.append(figure\Box{
            dir = b.dir, group = b.group, actor = actor(b.actor, at: "{at}boxes[{i}].actor"),
            label = b.label, sub = b.sub, tag = b.tag, focal = b.focal,
            look = look(b.look, at: "{at}boxes[{i}].look"), symbol = b.symbol,
        });
        i = i + 1;
    }
    final marks: mut [figure\EdgeMark] = mut [<figure\EdgeMark>];
    i = 0;
    while (i < f.edgeMarks.len()) {
        final m = f.edgeMarks[i];
        marks.append(figure\EdgeMark{ src = m.src, dst = m.dst, label = m.label, stroke = stroke(m.stroke, at: "{at}edgeMarks[{i}].stroke") });
        i = i + 1;
    }
    final flows: mut [figure\Flow] = mut [<figure\Flow>];
    i = 0;
    while (i < f.flows.len()) {
        final fl = f.flows[i];
        flows.append(figure\Flow{
            src = figure\End{ dir = fl.src.dir, actor = actor(fl.src.actor, at: "{at}flows[{i}].src.actor") },
            dst = figure\End{ dir = fl.dst.dir, actor = actor(fl.dst.actor, at: "{at}flows[{i}].dst.actor") },
            label = fl.label, stroke = stroke(fl.stroke, at: "{at}flows[{i}].stroke"),
        });
        i = i + 1;
    }
    final zones: mut [figure\Zone] = mut [<figure\Zone>];
    i = 0;
    while (i < f.zones.len()) {
        final z = f.zones[i];
        zones.append(figure\Zone{ label = z.label, dirs = z.dirs, actors = actors(z.actors, at: "{at}zones[{i}].actors"), boundary = z.boundary });
        i = i + 1;
    }
    final alignments: mut [figure\Alignment] = mut [<figure\Alignment>];
    i = 0;
    while (i < f.alignments.len()) {
        final a = f.alignments[i];
        alignments.append(figure\Alignment{
            axis = axis(a.axis, at: "{at}alignments[{i}].axis"), dirs = a.dirs,
            actors = actors(a.actors, at: "{at}alignments[{i}].actors"),
        });
        i = i + 1;
    }
    final legends: mut [figure\Legend] = mut [<figure\Legend>];
    i = 0;
    while (i < f.legends.len()) {
        final l = f.legends[i];
        final named = look(l.look, at: "{at}legends[{i}].look");
        if (named == null) { throw "{at}legends[{i}].look is missing; name a Look case"; }
        legends.append(figure\Legend{ look = named!, label = l.label });
        i = i + 1;
    }
    return figure\Figure{
        id = f.id, title = f.title, eyebrow = f.eyebrow, desc = f.desc,
        direction = direction(f.direction, at: "{at}direction"),
        generated = f.generated, unscopedWhy = f.unscopedWhy, graphEdges = f.graphEdges,
        boxes = boxes, scopes = f.scopes, exclusions = f.exclusions, hiddenEdges = f.hiddenEdges,
        edgeMarks = marks, flows = flows, zones = zones, alignments = alignments, legends = legends,
    };
}
`

// Draw lays fig out with figure\draw and paints it with Theme.page, the palette that
// follows a page's CSS variables. anchorHref is figure's link template, {path} and {line}.
//
// sess must resolve "magus/figure" and declare the magus\ record types the module names.
// Draw executes its entry program in sess, so a caller gives it a session of its own. A
// figure the module refuses, or a case name no enum holds, is a *Findings.
func Draw(ctx context.Context, sess *buzz.Session, fig Figure, anchorHref string) (string, error) {
	if err := sess.Exec(ctx, drawEntry); err != nil {
		return "", fmt.Errorf("figure: load the draw entry: %w", err)
	}
	fn, ok := sess.Exports()["draw"]
	if !ok {
		return "", errors.New("figure: the draw entry exported no draw")
	}
	v, err := sess.CallValue(ctx, fn, []vm.Value{fig.value(), vm.StrValue(anchorHref)})
	if err != nil {
		return "", fmt.Errorf("figure: draw %s: %w", fig.ID, err)
	}
	if !v.IsList() || len(v.ListItems()) != 2 {
		return "", fmt.Errorf("figure: draw %s returned %s", fig.ID, v.Kind())
	}
	items := v.ListItems()
	if findings := items[1].AsString(); findings != "" {
		return "", &Findings{Text: findings}
	}
	return items[0].AsString(), nil
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

func (f Figure) value() vm.Value {
	return record([]field{
		{"id", vm.StrValue(f.ID)},
		{"title", vm.StrValue(f.Title)},
		{"eyebrow", vm.StrValue(f.Eyebrow)},
		{"desc", vm.StrValue(f.Desc)},
		{"direction", vm.StrValue(string(f.Direction))},
		{"generated", vm.BoolValue(f.Generated)},
		{"unscopedWhy", vm.StrValue(f.UnscopedWhy)},
		{"graphEdges", vm.BoolValue(f.GraphEdges)},
		{"boxes", list(f.Boxes, Box.value)},
		{"scopes", list(f.Scopes, DirSet.value)},
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
		{"group", optional(b.Group, DirSet.value)},
		{"actor", optional(b.Actor, Actor.value)},
		{"label", vm.StrValue(b.Label)},
		{"sub", vm.StrValue(b.Sub)},
		{"tag", vm.StrValue(b.Tag)},
		{"focal", vm.BoolValue(b.Focal)},
		{"look", optional(b.Look, caseValue[Look])},
		{"symbol", vm.Null},
	})
}

func (s DirSet) value() vm.Value {
	return record([]field{{"dirs", list(s.Dirs, Dir.value)}, {"named", vm.StrValue(s.Named)}})
}

func (a Actor) value() vm.Value {
	return record([]field{
		{"name", vm.StrValue(a.Name)},
		{"sub", vm.StrValue(a.Sub)},
		{"tag", vm.StrValue(a.Tag)},
		{"link", vm.StrValue(a.Link)},
		{"look", optional(a.Look, caseValue[Look])},
	})
}

func (e Exclusion) value() vm.Value {
	return record([]field{{"set", e.Set.value()}, {"why", vm.StrValue(e.Why)}})
}

func (h HiddenEdges) value() vm.Value {
	return record([]field{{"src", h.Src.value()}, {"dst", h.Dst.value()}, {"why", vm.StrValue(h.Why)}})
}

func (m EdgeMark) value() vm.Value {
	return record([]field{
		{"src", m.Src.value()},
		{"dst", m.Dst.value()},
		{"label", vm.StrValue(m.Label)},
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
		{"label", vm.StrValue(f.Label)},
		{"stroke", optional(f.Stroke, caseValue[Stroke])},
	})
}

func (z Zone) value() vm.Value {
	return record([]field{
		{"label", vm.StrValue(z.Label)},
		{"dirs", optional(z.Dirs, DirSet.value)},
		{"actors", list(z.Actors, Actor.value)},
		{"boundary", vm.BoolValue(z.Boundary)},
	})
}

func (a Alignment) value() vm.Value {
	return record([]field{
		{"axis", vm.StrValue(string(a.Axis))},
		{"dirs", optional(a.Dirs, DirSet.value)},
		{"actors", list(a.Actors, Actor.value)},
	})
}

func (l Legend) value() vm.Value {
	return record([]field{{"look", vm.StrValue(string(l.Look))}, {"label", vm.StrValue(l.Label)}})
}

func (d Dir) value() vm.Value {
	return record([]field{
		{"path", vm.StrValue(d.Path)},
		{"id", vm.StrValue(d.ID)},
		{"layer", vm.StrValue(d.Layer)},
		{"language", vm.StrValue(d.Language)},
		{"imports", list(d.Imports, vm.StrValue)},
		{"importedBy", list(d.ImportedBy, vm.StrValue)},
		{"importsIndexed", vm.BoolValue(d.ImportsIndexed)},
		{"calls", list(d.Calls, DirCall.value)},
		{"calledBy", list(d.CalledBy, DirCall.value)},
		{"children", list(d.Children, vm.StrValue)},
		{"files", vm.IntValue(int64(d.Files))},
	})
}

func (c DirCall) value() vm.Value {
	return record([]field{
		{"dir", vm.StrValue(c.Dir)},
		{"transport", vm.StrValue(c.Transport)},
		{"marker", vm.StrValue(c.Marker)},
		{"source", vm.StrValue(c.Source)},
	})
}

// caseValue is an enum case's name as the str the entry decodes.
func caseValue[T ~string](c T) vm.Value { return vm.StrValue(string(c)) }

// list is never null: figure iterates every list field a host hands it.
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
