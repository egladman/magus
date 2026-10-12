package proofread

import (
	"fmt"
	"slices"
	"strings"
	"text/template/parse"
)

// A skill body is the text/template internal/agent renders twice: once short,
// the copy most sessions load, and once full. An `{{if .Full}}` arm is in the
// full copy only, its `{{else}}` arm in the short copy only, and `.Short` and
// `.Is "name"` branch the same way. The short copy is held to the terse rules
// as well as the Markdown ones; text only the full copy shows is held to the
// Markdown rules and [RuleBareRule], which holds for every word of a skill.

// skillForm is one render of a skill body, with the source line each of its
// lines starts on.
type skillForm struct {
	text string
	// lines holds the 1-based source line of each line of text.
	lines []int
}

// judgeSkillSource judges a skill body in both of its forms. The two share
// most of their text, so a Markdown finding both report is reported once. A
// body that does not render is judged by [RuleTemplate] alone, and passes
// where that rule is off.
func judgeSkillSource(text string, o options) []Finding {
	unrendered := func(message string) []Finding {
		d := o.decide(templateCheck, KindAgentInstructionsTemplate)
		if d == DecisionOff {
			return nil
		}

		return []Finding{o.settle(templateCheck, Finding{Message: message}, d)}
	}

	tree, err := parseSkill(text)
	if err != nil {
		return unrendered(fmt.Sprintf("The skill body does not render: %v. Balance its {{if .Full}} arms the way "+
			"internal/agent's template parser reads them.", err))
	}

	var forms [2]skillForm

	for i, full := range []bool{false, true} {
		forms[i], err = renderSkill(text, tree, full)
		if err != nil {
			return unrendered(fmt.Sprintf(
				"The skill body does not render: %v. A branch is .Full, .Short or .Is \"name\".", err))
		}
	}

	var found []Finding

	type key struct {
		rule           Rule
		message, match string
		line           int
		decision       Decision
	}

	seen := map[key]bool{}
	judged := append(judgeForm(forms[0], KindAgentInstructions, o), judgeForm(forms[1], KindReference, o)...)

	for _, f := range judgeForm(forms[1], KindAgentInstructions, o) {
		if f.Rule == RuleBareRule {
			judged = append(judged, f)
		}
	}

	source := input{source: strings.Split(text, "\n")}

	for _, f := range judged {
		k := key{f.Rule, f.Message, f.Match, f.Line, f.Decision}
		if !seen[k] {
			seen[k] = true
			found = append(found, source.locate(f))
		}
	}

	order := Rules()
	slices.SortStableFunc(found, func(a, b Finding) int {
		if d := slices.Index(order, a.Rule) - slices.Index(order, b.Rule); d != 0 {
			return d
		}

		return a.Line - b.Line
	})

	return found
}

// judgeForm runs kind's rules over a render and moves each finding to the
// source line it came from.
func judgeForm(form skillForm, kind Kind, o options) []Finding {
	found := judgeText(form.text, kind, o)

	for i := range found {
		if l := found[i].Line; l > 0 && l <= len(form.lines) {
			found[i].Line = form.lines[l-1]
		}

		// The columns were found in the rendered form; the caller places
		// the finding in the source again.
		found[i].Column, found[i].EndLine, found[i].EndColumn = 0, 0, 0
	}

	return found
}

// parseSkill parses text as internal/agent parses a skill body. Its lookups
// (`cmd`, `skill`, `buzz` and the rest) are registered there, not here, so a
// call is not checked against a function list.
func parseSkill(text string) (*parse.Tree, error) {
	tree := parse.New("skill")
	tree.Mode = parse.SkipFuncCheck

	return tree.Parse(text, "", "", map[string]*parse.Tree{})
}

// renderSkill renders tree as the full or the short form. It stops short of
// tidying blank lines the way internal/agent does: a run of blank lines reads
// as one paragraph break either way, and the source lines stay countable.
func renderSkill(text string, tree *parse.Tree, full bool) (skillForm, error) {
	var (
		out strings.Builder
		at  []int // at[i] is the source offset of out's byte i
	)

	emit := func(s string, pos int) {
		out.WriteString(s)

		for i := range len(s) {
			at = append(at, pos+i)
		}
	}

	var walk func(list *parse.ListNode) error

	walk = func(list *parse.ListNode) error {
		if list == nil {
			return nil
		}

		for _, node := range list.Nodes {
			switch n := node.(type) {
			case *parse.TextNode:
				emit(string(n.Text), int(n.Pos))
			case *parse.ActionNode:
				emit(lookupText(n.Pipe), int(n.Pos))
			case *parse.IfNode:
				taken, err := branchTaken(n.Pipe, full)
				if err != nil {
					return err
				}

				arm := n.ElseList
				if taken {
					arm = n.List
				}

				if err := walk(arm); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%s is not a branch", node)
			}
		}

		return nil
	}

	if err := walk(tree.Root); err != nil {
		return skillForm{}, err
	}

	return skillForm{text: out.String(), lines: sourceLines(text, out.String(), at)}, nil
}

// branchTaken evaluates an `if` the way the template does against a variant:
// `.Full`, `.Short`, or `.Is "full"` and `.Is "short"`, the only branches
// internal/agent accepts.
func branchTaken(pipe *parse.PipeNode, full bool) (bool, error) {
	if len(pipe.Cmds) == 1 {
		args := pipe.Cmds[0].Args
		field, ok := args[0].(*parse.FieldNode)

		switch {
		case !ok || len(field.Ident) != 1:
		case len(args) == 1 && field.Ident[0] == "Full":
			return full, nil
		case len(args) == 1 && field.Ident[0] == "Short":
			return !full, nil
		case len(args) == 2 && field.Ident[0] == "Is":
			if name, ok := args[1].(*parse.StringNode); ok {
				return name.Text == map[bool]string{true: "full", false: "short"}[full], nil
			}
		}
	}

	return false, fmt.Errorf("{{if %s}} is not a branch on the form", pipe)
}

// lookupText stands in for what an action renders: a string literal renders
// as itself, and a lookup such as {{cmd "agent install"}} as its arguments,
// which are the name it resolves.
func lookupText(pipe *parse.PipeNode) string {
	var words []string

	for _, cmd := range pipe.Cmds {
		for _, arg := range cmd.Args {
			if s, ok := arg.(*parse.StringNode); ok {
				words = append(words, s.Text)
			}
		}
	}

	return strings.Join(words, " ")
}

// sourceLines returns the source line each line of rendered starts on. A line
// starts after a newline, so its first byte sits on the source line it reads
// from; an empty last line takes the line after its newline's.
func sourceLines(source, rendered string, at []int) []int {
	var breaks []int // the offset of each newline in source

	for i := range len(source) {
		if source[i] == '\n' {
			breaks = append(breaks, i)
		}
	}

	lineOf := func(offset int) int {
		n, _ := slices.BinarySearch(breaks, offset)

		return n + 1
	}

	var out []int

	start := 0

	for {
		switch {
		case start < len(rendered):
			out = append(out, lineOf(at[start]))
		case len(at) > 0:
			out = append(out, lineOf(at[len(at)-1]+1))
		default:
			out = append(out, 1)
		}

		next := strings.IndexByte(rendered[start:], '\n')
		if next < 0 {
			return out
		}

		start += next + 1
	}
}
