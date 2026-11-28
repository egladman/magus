//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/emit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Emit is the "emit" host module, splice's write side: it renders new text from a
// template file with typed holes. No method raises. A plan only creates files, so
// a file that exists with other bytes is refused.
var Emit = Module{
	Name: "emit",
	Doc:  "Code templates as values with typed holes: load a template file, fill its holes, render it for an output path and plan new files for splice to preview and apply.",
	Methods: []Method{
		{
			Name:    "load",
			Doc:     "Read a template file and bind every hole to its value. values must name every hole and nothing else, and no value may be null. Reads through the fs read policy and writes nothing. The Result's error lists every problem in the file, one per line.",
			Args:    []Arg{{Name: "path"}, {Name: "values"}},
			Returns: []Ret{{Object: `result\Result::<Template>`}},
			Impl:    EmitLoad,
		},
		{
			Name:    "text",
			Doc:     "A str as static text: a template with no holes. No hole is parsed out of text.",
			Args:    []Arg{{Name: "text"}},
			Returns: []Ret{{Object: "Template"}},
			Impl:    EmitText,
		},
		{
			Name:    "hole",
			Doc:     "A str or scalar as one hole with its encoding: a template of one hole. An int, a double or a bool is written in its string form, as !s writes it. expression names the hole in a refusal.",
			Args:    []Arg{{Name: "value"}, {Name: "spec"}, {Name: "expression", Optional: true, Default: ""}},
			Returns: []Ret{{Object: "Template"}},
			Impl:    EmitHole,
		},
		{
			Name:    "join",
			Doc:     "Parts joined with sep between them. sep is static text, and sep \"\" is template concatenation.",
			Args:    []Arg{{Name: "parts", Object: "[Template]"}, {Name: "sep"}},
			Returns: []Ret{{Object: "Template"}},
			Impl:    EmitJoin,
		},
		{
			Name:    "render",
			Doc:     "The text a template becomes as the file at path. path's extension picks the encodings through the workspace's spell languages; no file is read or written.",
			Args:    []Arg{{Name: "template", Object: "Template"}, {Name: "path"}},
			Returns: []Ret{{Object: `result\Result::<str>`}},
			Impl:    EmitRender,
		},
		{
			Name:    "file",
			Doc:     "A file to write: path, resolved as fs paths are when planned, and the template it holds.",
			Args:    []Arg{{Name: "path"}, {Name: "template", Object: "Template"}},
			Returns: []Ret{{Object: "File"}},
			Impl:    EmitFile,
		},
		{
			Name:    "plan",
			Doc:     "Render every file and resolve each as a new file under the lease, writing nothing. A path named twice, an output with no final newline and a path that exists with other bytes are refused. Refusals are fields of the plan; splice\\diff previews it and splice\\apply writes it.",
			Args:    []Arg{{Name: "files", Object: "[File]"}},
			Returns: []Ret{{Object: `splice\SplicePlan`}},
			Impl:    EmitPlan,
		},
	},
}

// EmitLoad is emit\load.
func EmitLoad(ctx context.Context, path string, values map[string]any) (types.Result[types.Template], error) {
	src, err := FsReadFile(ctx, path)
	if err != nil {
		return types.Fail[types.Template](errorDiagnostic(err)), nil
	}
	t, problems := emit.Load(path, []byte(src), values)
	if len(problems) > 0 {
		return types.Fail[types.Template](types.Diagnostic{
			Code:    string(problems[0].Code),
			Message: strings.Join(problems.Lines(path), "\n"),
			URL:     types.CodeURL(problems[0].Code),
			Hint:    "fix each problem in the template or in values, then load again",
		}), nil
	}
	return types.Ok(t), nil
}

// EmitText is emit\text.
func EmitText(_ context.Context, text string) (types.Template, error) {
	return types.Template{Strings: []string{text}}, nil
}

// EmitHole is emit\hole.
func EmitHole(_ context.Context, value any, spec, expression string) (types.Template, error) {
	h := types.Interpolation{Value: value, Expression: expression, FormatSpec: spec}
	switch value.(type) {
	case bool, int, int64, float64:
		h.Conversion = "s"
	}
	return types.Template{Strings: []string{"", ""}, Interpolations: []types.Interpolation{h}}, nil
}

// EmitJoin is emit\join.
func EmitJoin(_ context.Context, parts []types.Template, sep string) (types.Template, error) {
	return emit.Join(parts, sep), nil
}

// EmitRender is emit\render.
func EmitRender(ctx context.Context, t types.Template, path string) (types.Result[string], error) {
	index, missing := encodingIndex(ctx)
	if missing != nil {
		return types.Fail[string](*missing), nil
	}
	out, problems := emit.Render(fromBuzz(t), path, index)
	if len(problems) > 0 {
		return types.Fail[string](types.Diagnostic{
			Code:    string(problems[0].Code),
			Message: strings.Join(problems.Lines(templateName(t)), "\n"),
			URL:     types.CodeURL(problems[0].Code),
			Hint:    "fix each problem in the template or its values, then render again",
		}), nil
	}
	return types.Ok(out), nil
}

// EmitFile is emit\file.
func EmitFile(_ context.Context, path string, t types.Template) (types.EmitFile, error) {
	return types.EmitFile{Path: path, Template: t}, nil
}

// EmitPlan is emit\plan. Every outcome, a refusal included, is the returned plan.
func EmitPlan(ctx context.Context, files []types.EmitFile) (types.EditPlan, error) {
	index, missing := encodingIndex(ctx)
	if missing != nil {
		return refusedPlan(nil, []types.EditRefusal{{Rule: missing.Code, Reason: fmt.Sprintf("%s: %s", missing.Message, missing.Hint)}}), nil
	}
	var refused []types.EditRefusal
	changes := make([]types.EditChange, 0, len(files))
	first := map[string]int{}
	exists := map[string]bool{}
	for i, f := range files {
		abs := resolvePath(ctx, f.Path)
		if j, seen := first[abs]; seen {
			refused = append(refused, types.EditRefusal{Path: f.Path, Rule: string(types.EmitDuplicatePath),
				Reason: fmt.Sprintf("is written twice (files %d and %d): keep one file per path", j+1, i+1)})
			continue
		}
		first[abs] = i
		out, problems := emit.Render(fromBuzz(f.Template), f.Path, index)
		if len(problems) > 0 {
			refused = append(refused, renderRefusals(f, problems)...)
			continue
		}
		if out != "" && !strings.HasSuffix(out, "\n") {
			refused = append(refused, types.EditRefusal{Path: f.Path, Rule: string(types.EmitNoFinalNewline),
				Reason: fmt.Sprintf("does not end in a line feed: end %s with a newline", templateName(f.Template))})
			continue
		}
		if existsWithOther(ctx, f.Path, out) {
			exists[f.Path] = true
			refused = append(refused, types.EditRefusal{Path: f.Path, Rule: string(types.EditPlanRefused),
				Reason: "exists and holds other bytes, and emit writes only new files: change it with splice, or remove it and plan again"})
		}
		changes = append(changes, types.EditChange{Path: f.Path, New: out, Create: true})
	}
	if len(changes) == 0 && len(refused) > 0 {
		return refusedPlan(changes, refused), nil
	}
	p, err := SplicePlan(ctx, changes)
	if err != nil {
		return types.EditPlan{}, err
	}
	if len(refused) == 0 {
		return p, nil
	}
	// splice refuses an existing path too, with a fix that names old and new;
	// emit's refusal for that path replaces it.
	for _, r := range p.Refused {
		if !exists[r.Path] {
			refused = append(refused, r)
		}
	}
	return refusedPlan(changes, refused), nil
}

// existsWithOther reports a readable path that holds bytes other than content. A
// path that cannot be read is left to splice, which names why.
func existsWithOther(ctx context.Context, path, content string) bool {
	body, err := FsReadFile(ctx, path)
	return err == nil && body != content
}

func refusedPlan(changes []types.EditChange, refused []types.EditRefusal) types.EditPlan {
	if changes == nil {
		changes = []types.EditChange{}
	}
	return types.EditPlan{
		Changes: changes,
		Files:   []types.EditedFile{},
		Spans:   []types.EditSpan{},
		Refused: refused,
	}
}

// renderRefusals places each problem in the template file when it came from one,
// and on the output path when the template was built in Buzz.
func renderRefusals(f types.EmitFile, problems emit.Problems) []types.EditRefusal {
	out := make([]types.EditRefusal, len(problems))
	for i, p := range problems {
		r := types.EditRefusal{Path: f.Path, Rule: string(p.Code), Reason: p.Message}
		if f.Template.Source != "" {
			r = types.EditRefusal{Path: f.Template.Source, Line: p.Line, Column: p.Column, Rule: string(p.Code),
				Reason: fmt.Sprintf("%s (writing %s)", p.Message, f.Path)}
		}
		out[i] = r
	}
	return out
}

func templateName(t types.Template) string {
	if t.Source != "" {
		return t.Source
	}
	return "a template built in Buzz"
}

func errorDiagnostic(err error) types.Diagnostic {
	var coded *types.DiagnosticError
	if errors.As(err, &coded) {
		return types.Diagnostic{Code: string(coded.Code), Message: coded.Msg, URL: coded.URL(), Hint: coded.Hint}
	}
	return types.Diagnostic{Message: err.Error()}
}

// encodingIndex routes each extension to the first language claiming it, as
// spells.EncodingIndex does. It is a copy because the run carries languages, not
// the spells EncodingIndex reads, so the spell's name is not known here.
func encodingIndex(ctx context.Context) (map[string]spells.LanguageEncodings, *types.Diagnostic) {
	langs, ok := LanguagesFromContext(ctx)
	if !ok {
		return nil, &types.Diagnostic{
			Code:    string(types.EmitUnknownEncoding),
			Message: "this run carries no spell languages, so no hole's encoding can be resolved",
			URL:     types.CodeURL(types.EmitUnknownEncoding),
			Hint:    "run the script with magus buzz, from a target or through the MCP Buzz tool, which carry the workspace's languages",
		}
	}
	out := map[string]spells.LanguageEncodings{}
	for _, l := range langs {
		var encs []spells.Encoding
		if l.Syntax != nil {
			encs = l.Syntax.Encodings
		}
		for _, ext := range l.Extensions {
			if _, taken := out[ext]; !taken {
				out[ext] = spells.LanguageEncodings{Language: l.Name, Encodings: encs}
			}
		}
	}
	return out, nil
}

// templateKeys are the members a Template object crosses from Buzz with. A hole's
// value crosses as plain data, so a nested template arrives as a map of these keys.
var templateKeys = []string{"interpolations", "source", "strings"}

// fromBuzz turns every template-shaped map among t's hole values into a
// types.Template, at any depth, so the renderer sees nesting as Go built it.
func fromBuzz(t types.Template) types.Template {
	out := t
	out.Interpolations = slices.Clone(t.Interpolations)
	for i, h := range out.Interpolations {
		out.Interpolations[i].Value = valueFromBuzz(h.Value)
	}
	return out
}

func valueFromBuzz(v any) any {
	switch x := v.(type) {
	case types.Template:
		return fromBuzz(x)
	case map[string]any:
		if t, ok := templateFromMap(x); ok {
			return fromBuzz(t)
		}
	}
	return v
}

// templateFromMap reads m as a Template when its keys are exactly a Template's and
// each member has a Template's shape. Anything else stays a map, which the
// renderer refuses with its kind.
func templateFromMap(m map[string]any) (types.Template, bool) {
	if len(m) != len(templateKeys) {
		return types.Template{}, false
	}
	for _, k := range templateKeys {
		if _, ok := m[k]; !ok {
			return types.Template{}, false
		}
	}
	source, isStr := m["source"].(string)
	strs, isList := m["strings"].([]any)
	holes, isHoles := m["interpolations"].([]any)
	if !isStr || !isList || !isHoles {
		return types.Template{}, false
	}
	t := types.Template{Source: source, Strings: make([]string, len(strs))}
	for i, s := range strs {
		str, ok := s.(string)
		if !ok {
			return types.Template{}, false
		}
		t.Strings[i] = str
	}
	for _, h := range holes {
		hm, isMap := h.(map[string]any)
		if !isMap {
			return types.Template{}, false
		}
		expr, _ := hm["expression"].(string)
		conv, _ := hm["conversion"].(string)
		spec, _ := hm["formatSpec"].(string)
		t.Interpolations = append(t.Interpolations, types.Interpolation{
			Value: hm["value"], Expression: expr, Conversion: conv, FormatSpec: spec,
		})
	}
	return t, true
}
