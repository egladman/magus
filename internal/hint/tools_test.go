package hint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The tool parsers are graded on REAL invocations from both coreutils families, because
// the two disagree in exactly the places a guard reads. Every case below names which
// family spells it that way, so a reader can tell a deliberate BSD-ism from a typo.
//
// The rule these serve is a safety one, so the bias is stated once here rather than per
// case: where a spelling is ambiguous ACROSS families, the answer is the unsafe reading.
// A parser that guesses right on GNU and wrong on BSD is worse than one that declines,
// because the wrong guess is silent on half the machines that run it.

// TestVariantInferenceReadsOnlyFamilySpecificSpellings pins what the flag evidence can and
// cannot decide. The pair with LocalVariant is the design: one says what the author
// assumed, the other what will run it, and only their DISAGREEMENT is interesting.
func TestVariantInferenceReadsOnlyFamilySpecificSpellings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  Invocation
		want Variant
	}{
		// Long options are the strongest signal: BSD sed and BSD xargs have none.
		{name: "sed long in-place is GNU", cmd: Invocation{Name: "sed", Args: []string{"--in-place", "s/a/b/"}}, want: VariantGNU},
		{name: "sed long in-place with value is GNU", cmd: Invocation{Name: "sed", Args: []string{"--in-place=.bak"}}, want: VariantGNU},
		{name: "xargs -r is GNU", cmd: Invocation{Name: "xargs", Args: []string{"-r", "rm"}}, want: VariantGNU},
		{name: "xargs --null is GNU", cmd: Invocation{Name: "xargs", Args: []string{"--null", "rm"}}, want: VariantGNU},
		{name: "grep -P is GNU", cmd: Invocation{Name: "grep", Args: []string{"-P", `\d+`}}, want: VariantGNU},
		{name: "find -printf is GNU", cmd: Invocation{Name: "find", Args: []string{".", "-printf", "%p"}}, want: VariantGNU},

		{name: "xargs -J is BSD", cmd: Invocation{Name: "xargs", Args: []string{"-J", "%", "cp"}}, want: VariantBSD},
		{name: "xargs -L is BSD", cmd: Invocation{Name: "xargs", Args: []string{"-L", "1", "rm"}}, want: VariantBSD},

		// Nothing family-specific, so no claim. Saying "GNU" here because it is the common
		// case would be inventing evidence.
		{name: "portable sed decides nothing", cmd: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "f"}}, want: VariantUnknown},
		{name: "portable grep decides nothing", cmd: Invocation{Name: "grep", Args: []string{"-rn", "foo", "."}}, want: VariantUnknown},
		{name: "a tool with no table entry", cmd: Invocation{Name: "awk", Args: []string{"{print}"}}, want: VariantUnknown},

		// Evidence both ways is a line no single tool would accept; unknown is truer than
		// picking a winner.
		{name: "both families named at once", cmd: Invocation{Name: "xargs", Args: []string{"-r", "-J", "%", "cp"}}, want: VariantUnknown},

		{name: "an absolute path resolves by base name",
			cmd: Invocation{Name: "/usr/bin/xargs", Args: []string{"-r", "rm"}}, want: VariantGNU},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, InferVariant(tc.cmd))
		})
	}
}

// TestPortabilityGapNamesBothSides pins that the gap is reported only when both sides are
// known AND disagree. It is context, never a refusal: the command may be headed for a
// container, which is a machine this cannot see.
func TestPortabilityGapNamesBothSides(t *testing.T) {
	t.Parallel()

	gnuSed := Invocation{Name: "sed", Args: []string{"--in-place", "s/a/b/", "f"}}
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "gnu")
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "bsd")
	assert.Contains(t, PortabilityGap(gnuSed, VariantBSD), "sed")

	assert.Empty(t, PortabilityGap(gnuSed, VariantGNU), "agreement is not a gap")
	assert.Empty(t, PortabilityGap(gnuSed, VariantUnknown), "an unknown host cannot disagree")
	assert.Empty(t, PortabilityGap(Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "f"}}, VariantBSD),
		"a portable spelling names no family, so there is nothing to compare")
}

// TestLocalVariantIsStableAndDerivedOnce pins the session-scoped contract: one answer for
// the process, so nothing recomputes it per line.
func TestLocalVariantIsStableAndDerivedOnce(t *testing.T) {
	t.Parallel()
	assert.Equal(t, LocalVariant(), LocalVariant())
	assert.Equal(t, LocalVariant(), NewTranslator().Variant(),
		"a translator starts from the host answer, so the common case needs no option")
	assert.Equal(t, VariantBSD, NewTranslator(WithVariant(VariantBSD)).Variant(),
		"an explicit override wins, for a caller reasoning about another machine")
}

func TestSedFilesAcrossCoreutilsFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		args    []string
		files   []string
		bounded bool
	}{
		// The ambiguity that motivates the whole function. BSD sed reads the word after a
		// bare -i as the backup SUFFIX; GNU sed reads it as the first FILE. No parser can
		// tell them apart, so the bare form is never bounded.
		{name: "bare -i is unsplittable (BSD suffix vs GNU file)", args: []string{"-i", "", "s/a/b/", "f.go"}},
		{name: "bare -i even with obvious files", args: []string{"-i", "s/a/b/", "one.go", "two.go"}},
		{name: "bare -i last", args: []string{"-e", "s/a/b/", "-i", "one.go"}},

		// Packed suffix is unambiguous in both families.
		{name: "GNU packed suffix", args: []string{"-i.bak", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},
		{name: "BSD packed suffix", args: []string{"-i''", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},
		{name: "packed suffix, several files", args: []string{"-i.bak", "s/a/b/", "a.go", "b.go", "c.go"},
			files: []string{"a.go", "b.go", "c.go"}, bounded: true},

		// --in-place is GNU only; BSD sed has no long options at all.
		{name: "GNU long form takes no suffix", args: []string{"--in-place", "s/a/b/", "one.go"},
			files: []string{"one.go"}, bounded: true},
		{name: "GNU long form with inline suffix", args: []string{"--in-place=.bak", "s/a/b/", "one.go"},
			files: []string{"one.go"}, bounded: true},

		// -e and -f supply the script, so the first bare word is then a FILE, not a script.
		// Reading it as a script would drop a real file from the list and under-report.
		{name: "-e supplies the script so no operand is consumed", args: []string{"-i.bak", "-e", "s/a/b/", "one.go", "two.go"},
			files: []string{"one.go", "two.go"}, bounded: true},
		{name: "several -e scripts", args: []string{"-i.bak", "-e", "s/a/b/", "-e", "s/c/d/", "one.go"},
			files: []string{"one.go"}, bounded: true},
		{name: "-f names a script FILE, which is not an edited file", args: []string{"-i.bak", "-f", "prog.sed", "one.go"},
			files: []string{"one.go"}, bounded: true},

		// A script arriving as the first bare word is consumed, leaving the rest as files.
		{name: "script then file", args: []string{"-i.bak", "s/a/b/", "one.go"}, files: []string{"one.go"}, bounded: true},

		// Unbounded operand sets: the edited files are not visible on the line.
		{name: "recursive glob", args: []string{"-i.bak", "s/a/b/", "**/*.go"}},
		{name: "simple glob", args: []string{"-i.bak", "s/a/b/", "*.go"}},
		{name: "character class", args: []string{"-i.bak", "s/a/b/", "file[0-9].go"}},
		{name: "brace expansion", args: []string{"-i.bak", "s/a/b/", "{a,b}.go"}},
		{name: "command substitution", args: []string{"-i.bak", "s/a/b/", "$(git ls-files)"}},
		{name: "backtick substitution", args: []string{"-i.bak", "s/a/b/", "`git ls-files`"}},
		{name: "one bad operand poisons the set", args: []string{"-i.bak", "s/a/b/", "ok.go", "*.go"}},

		// No file at all: sed reads a stream, and what it edits is upstream's business.
		{name: "script only, files come from a pipe", args: []string{"-i.bak", "s/a/b/"}},
		{name: "no arguments", args: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, bounded := SedFiles(tc.args)
			assert.Equal(t, tc.bounded, bounded, "bounded")
			if tc.bounded {
				assert.Equal(t, tc.files, files, "files")
			}
		})
	}
}

// TestDrivenCommandAcrossDriverFamilies grades the parser that finds the command a driver
// runs. It is the one that was missing, and its absence is why a driven rewrite read as
// harmless: the invocation magus parsed was the DRIVER, whose own operands say nothing
// about what the child will do.
func TestDrivenCommandAcrossDriverFamilies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  Invocation
		want Invocation
		ok   bool
	}{
		// xargs: everything after its own flags is the child's argv, and the child's flags
		// must survive. Stripping them is the bug this test exists for: `xargs sed -i`
		// came back as sed with no -i, so an in-place rewrite graded as a read.
		{name: "plain xargs keeps the child's flags",
			cmd:  Invocation{Name: "xargs", Args: []string{"sed", "-i", "", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i", "", "s/a/b/"}}, ok: true},
		{name: "GNU -r before the command",
			cmd:  Invocation{Name: "xargs", Args: []string{"-r", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "GNU --null long flag",
			cmd:  Invocation{Name: "xargs", Args: []string{"--null", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "-n consumes its count",
			cmd:  Invocation{Name: "xargs", Args: []string{"-n", "1", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "-P consumes its parallelism",
			cmd:  Invocation{Name: "xargs", Args: []string{"-P", "4", "rm", "-rf"}},
			want: Invocation{Name: "rm", Args: []string{"-rf"}}, ok: true},
		{name: "-I consumes its replace string",
			cmd:  Invocation{Name: "xargs", Args: []string{"-I", "{}", "sed", "-i.bak", "s/a/b/", "{}"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-0 is boolean and consumes nothing",
			cmd:  Invocation{Name: "xargs", Args: []string{"-0", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "several flags before the command",
			cmd:  Invocation{Name: "xargs", Args: []string{"-0", "-r", "-n", "1", "-P", "8", "sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
		{name: "bare xargs runs echo and writes nothing",
			cmd: Invocation{Name: "xargs", Args: nil}},
		{name: "flags but no command",
			cmd: Invocation{Name: "xargs", Args: []string{"-r", "-0"}}},

		// find: the command runs between -exec and its terminator. Anything past the
		// terminator is another predicate, and swallowing it would attribute flags to the
		// child that find never passes it.
		{name: "-exec with semicolon terminator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-name", "*.go", "-exec", "sed", "-i.bak", "s/a/b/", "{}", ";"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-exec with plus terminator",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", "+"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "escaped semicolon, as a shell line carries it",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", `\;`}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "-execdir is the same shape",
			cmd:  Invocation{Name: "find", Args: []string{".", "-execdir", "rm", "{}", ";"}},
			want: Invocation{Name: "rm", Args: []string{"{}"}}, ok: true},
		{name: "-ok prompts but still runs the command",
			cmd:  Invocation{Name: "find", Args: []string{".", "-ok", "rm", "{}", ";"}},
			want: Invocation{Name: "rm", Args: []string{"{}"}}, ok: true},
		{name: "predicates AFTER the terminator are not the child's argv",
			cmd:  Invocation{Name: "find", Args: []string{".", "-exec", "sed", "-i.bak", "s/a/b/", "{}", ";", "-print"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/", "{}"}}, ok: true},
		{name: "find with no -exec drives nothing",
			cmd: Invocation{Name: "find", Args: []string{".", "-name", "*.go", "-print"}}},
		{name: "-exec with nothing after it",
			cmd: Invocation{Name: "find", Args: []string{".", "-exec"}}},

		// Everything else drives nothing, including tools that merely read a file list.
		{name: "grep is not a driver", cmd: Invocation{Name: "grep", Args: []string{"-r", "foo", "."}}},
		{name: "an absolute path still resolves by base name",
			cmd:  Invocation{Name: "/usr/bin/xargs", Args: []string{"sed", "-i.bak", "s/a/b/"}},
			want: Invocation{Name: "sed", Args: []string{"-i.bak", "s/a/b/"}}, ok: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := DrivenCommand(tc.cmd)
			assert.Equal(t, tc.ok, ok, "found a driven command")
			if tc.ok {
				assert.Equal(t, tc.want.Name, got.Name, "driven command name")
				assert.Equal(t, tc.want.Args, got.Args, "driven command args")
			}
		})
	}
}
