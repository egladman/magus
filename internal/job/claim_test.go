package job

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefuseUngradableClaims(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n*.md diff=markdown\n", "run.go": "package run\n", "notes/a#b.md": "# A\n"}).Root()
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"no claim below a file reads nothing", []string{"run.go", "notes.txt"}, ""},
		{"a declaration of a driven file", []string{"run.go#executeStages", "docs/new.md#Usage"}, ""},
		{"a file with no driver", []string{"notes.txt#Intro"}, "\"notes.txt\" has no diff driver, so no changed line of it can be placed in a declaration: give `*.txt` one in .gitattributes"},
		{"an empty declaration", []string{"run.go#"}, `"run.go#" names no declaration after its #`},
		{"a pattern", []string{"*.go#executeStages"}, `"*.go#executeStages" claims a declaration of a pattern`},
		{"every bad entry at once", []string{"run.go#", "Makefile#all"}, `"Makefile" has no diff driver`},
		{"a path holding # spelled as a path", []string{"./notes/a#b.md", `notes/a\#b.md`, `notes/a\#b.md#A`}, ""},
		{"a path holding # left ambiguous", []string{"notes/a#b.md"},
			"\"notes/a#b.md\" is a path in this tree and also reads as a claim on \"notes/a\", since an unescaped # starts a claimed declaration: spell the path `./notes/a#b.md` or `notes/a\\#b.md`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, root))
			_, err := ForkMerge(context.Background(), s, "wave/job", func(u *types.Job) {
				u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathClaimUngradable)
			assert.Contains(t, err.Error(), tc.want)
			rows, lerr := s.List()
			require.NoError(t, lerr)
			assert.Empty(t, rows, "a refused fork writes no row")
		})
	}

	// The guard grades a deny path's declaration by the same placement, so a row may not
	// deny a declaration the guard would have to read as the whole file.
	for _, tc := range []struct {
		name string
		deny string
		want string
	}{
		{"a deny path on a driven file", "run.go#executeStages", ""},
		{"a deny path on a file with no driver", "notes.txt#Intro", "deny path \"notes.txt\" has no diff driver"},
		{"a deny path on a pattern", "*.go#executeStages", `deny path "*.go#executeStages" claims a declaration of a pattern`},
		{"a deny path with no declaration", "run.go#", `deny path "run.go#" names no declaration after its #`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, root))
			_, err := ForkMerge(context.Background(), s, "wave/job", func(u *types.Job) {
				u.Check, u.State, u.WritePaths, u.DenyPaths = forkCheck(), types.StateDeclared, []string{"run.go", "notes.txt"}, []string{tc.deny}
			}, config.Jobs{}, nil)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathClaimUngradable)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("a checkout with no version control", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tmpLoc(t, t.TempDir()))
		ps := ungradableClaimProblems(context.Background(), s, types.Job{ID: "wave/job", WritePaths: []string{"run.go#executeStages"}})
		require.Len(t, ps, 1)
		assert.Equal(t, types.WritePathClaimUngradable, ps[0].Code)
		assert.Equal(t, "write_paths[0]", ps[0].Field)
		assert.Contains(t, ps[0].Message, `"run.go": `)
	})

	t.Run("every bad entry is its own problem", func(t *testing.T) {
		t.Parallel()
		ps := ungradableClaimProblems(context.Background(), NewStore(tmpLoc(t, root)), types.Job{ID: "wave/job",
			WritePaths: []string{"run.go", "run.go#", "notes.txt#Intro"}, DenyPaths: []string{"*.go#x"}})
		fields := make([]string, len(ps))
		for i, p := range ps {
			fields[i] = p.Field
		}
		assert.Equal(t, []string{"write_paths[1]", "deny_paths[0]", "write_paths[2]"}, fields)
	})
}

func TestRefuseUnorderedFileShare(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n", "notes.txt": "hi\n", "docs/x.md": "# X\n"}
	bRoot := testkit.NewRepo(t, files).Root()
	aRepo := testkit.NewRepo(t, files)
	aRoot := aRepo.Root()
	aRev := aRepo.Head()

	for _, tc := range []struct {
		name        string
		aWrite      []string
		aCheckout   *string // nil: aRoot (a different checkout than the store's); &"": untaken
		aDependsOn  []string
		bWrite      []string
		bParent     string
		bDependsOn  []string
		wantRefused bool
	}{
		{name: "unordered whole-file share", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, wantRefused: true},
		{name: "different declarations of one file pass", aWrite: []string{"magusfile.buzz#lint_files"}, bWrite: []string{"magusfile.buzz#lint"}},
		{name: "depends_on orders the pair", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, bDependsOn: []string{"A"}},
		{name: "a child of the holder", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, bParent: "A"},
		{name: "a glob is not a literal file", aWrite: []string{"docs/x.md"}, bWrite: []string{"docs/**"}},
		{name: "a file with no diff driver", aWrite: []string{"notes.txt"}, bWrite: []string{"notes.txt"}},
		{name: "an untaken holder is left alone", aWrite: []string{"magusfile.buzz"}, aCheckout: strPtr(""), bWrite: []string{"magusfile.buzz"}},
		{name: "a holder in the same checkout is left alone", aWrite: []string{"magusfile.buzz"}, aCheckout: strPtr(bRoot), bWrite: []string{"magusfile.buzz"}},
		{name: "a holder blocked on depends_on is left alone", aWrite: []string{"magusfile.buzz"}, aDependsOn: []string{"C"}, bWrite: []string{"magusfile.buzz"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			loc := tmpLoc(t, bRoot)
			s := NewStore(loc)
			checkout := aRoot
			if tc.aCheckout != nil {
				checkout = *tc.aCheckout
			}
			a := types.Job{ID: "A", State: types.StateRunning, WritePaths: tc.aWrite, DependsOn: tc.aDependsOn, Checkpoint: aRev, CheckoutRoot: checkout}
			seed(t, s, a)
			rows, err := s.List()
			require.NoError(t, err)

			candidate := types.Job{ID: "B", WritePaths: tc.bWrite, Parent: tc.bParent, DependsOn: tc.bDependsOn}
			refErr := unorderedFileShareProblems(context.Background(), s, rows, candidate).Err()
			if !tc.wantRefused {
				require.NoError(t, refErr)
				return
			}
			require.ErrorIs(t, refErr, types.WritePathFileShared)
			assert.Contains(t, refErr.Error(), `B declares "magusfile.buzz", and A`)
			assert.Contains(t, refErr.Error(), "none yet")
			assert.Contains(t, refErr.Error(), "Claim `magusfile.buzz#<declaration>`")
			assert.Contains(t, refErr.Error(), "--depends-on A")
		})
	}
}

// A holder that exited is done editing, so the file its row still lists is no longer one
// a fork in another checkout shares with it (MGS3032); a running holder still refuses.
func TestExitedHolderClaimsNoFileShare(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n"}
	bRoot := testkit.NewRepo(t, files).Root()
	aRepo := testkit.NewRepo(t, files)
	aRoot := aRepo.Root()
	aRev := aRepo.Head()

	for state, refused := range map[types.JobState]bool{types.StateRunning: true, types.StateExited: false} {
		s := NewStore(tmpLoc(t, bRoot))
		seed(t, s, types.Job{ID: "A", State: state, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot})
		rows, err := s.List()
		require.NoError(t, err)

		err = unorderedFileShareProblems(context.Background(), s, rows, types.Job{ID: "B", WritePaths: []string{"magusfile.buzz"}}).Err()
		if refused {
			require.ErrorIs(t, err, types.WritePathFileShared, state)
			continue
		}
		require.NoError(t, err, state)
	}
}

// A re-fork that replaces a declared row is judged by the depends_on it declares, not by
// the stored row it replaces: adding the dependency the refusal asked for is the fix.
func TestRefuseUnorderedFileShareReadsTheReplacementsDependsOn(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n"}
	bRoot := testkit.NewRepo(t, files).Root()
	aRepo := testkit.NewRepo(t, files)
	aRoot := aRepo.Root()
	aRev := aRepo.Head()

	s := NewStore(tmpLoc(t, bRoot))
	seed(t, s, types.Job{ID: "A", State: types.StateRunning, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot})
	seed(t, s, types.Job{ID: "B", State: types.StateDeclared, WritePaths: []string{"notes.txt"}})
	rows, err := s.List()
	require.NoError(t, err)

	replacement := types.Job{ID: "B", WritePaths: []string{"magusfile.buzz"}, DependsOn: []string{"A"}}
	assert.Empty(t, unorderedFileShareProblems(context.Background(), s, rows, replacement))
	replacement.DependsOn = nil
	require.ErrorIs(t, unorderedFileShareProblems(context.Background(), s, rows, replacement).Err(), types.WritePathFileShared)
}

const claimGo = `package run

func A(ctx int) {
	a()
}

func B(ctx int) {
	b()
}

func C() {
	c()
}
`

var claimFiles = map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": claimGo}

// A claim word that several declarations of its file share guards all of them, not the one
// its author meant, so the fork is refused with words that name one each. A word that
// names none is accepted: the declaration may not be written yet.
func TestRefuseUngradableClaimsAmbiguousWord(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, claimFiles).Root()
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"a word one declaration has", []string{"run.go#A"}, ""},
		{"the whole declaration line", []string{"run.go#func C() {"}, ""},
		{"a declaration not written yet", []string{"run.go#helper"}, ""},
		{"a word several declarations share", []string{"run.go#ctx"},
			"\"run.go#ctx\" names 2 declarations of run.go (such as `func A(ctx int) {`), and a claim guards exactly one: use a word only one of them has, such as `A`, `B`"},
		{"a word every declaration shares", []string{"run.go#func"}, `"run.go#func" names 3 declarations of run.go`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, root))
			err := ungradableClaimProblems(context.Background(), s, types.Job{ID: "wave/job", WritePaths: tc.paths}).Err()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathClaimUngradable)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("a deny path is held to it", func(t *testing.T) {
		t.Parallel()
		err := ungradableClaimProblems(context.Background(), NewStore(tmpLoc(t, root)), types.Job{ID: "wave/job", DenyPaths: []string{"run.go#ctx"}}).Err()
		require.ErrorIs(t, err, types.WritePathClaimUngradable)
		assert.Contains(t, err.Error(), `deny path "run.go#ctx" names 2 declarations of run.go`)
	})
}

// The refusal prints the claim pair it computed, from the declarations the holder has
// touched and the symbols the new job's goals name, and says whether the two are disjoint.
func TestRefuseUnorderedFileSharePrintsTheClaimPair(t *testing.T) {
	t.Parallel()

	bRoot := testkit.NewRepo(t, claimFiles).Root()
	aRepo := testkit.NewRepo(t, claimFiles)
	aRev := aRepo.Head()
	aRepo.WriteTree(map[string]string{"run.go": strings.Replace(claimGo, "a()", "a2()", 1)})

	symbol := func(names ...string) []types.Goal {
		return []types.Goal{{ID: "g", Kind: types.GoalKindSymbol, Expect: types.ExpectChanged, Symbols: names}}
	}
	for _, tc := range []struct {
		name  string
		goals []types.Goal
		holds []string
		want  []string
	}{
		{"disjoint declarations", symbol("C"), []string{"run.go"}, []string{
			"A has touched func A(ctx int) { in it so far:",
			"Claim `run.go#A` on A and `run.go#C` on B, which name different declarations (nothing was written to either row), or add `--depends-on A` to B, or fold B into A",
		}},
		{"the same declaration", symbol("A"), []string{"run.go"}, []string{
			"B's symbol goals and A's touched work both reach A in it, so a claim cannot split them: add `--depends-on A` to B, or fold B into A",
		}},
		{"a symbol the file does not define", symbol("Missing"), []string{"run.go"}, []string{
			"Claim `run.go#A` on A (it has touched it) and a declaration other than that on B",
		}},
		{"no symbol goal at all", nil, []string{"run.go"}, []string{
			"Claim `run.go#A` on A (it has touched it) and a declaration other than that on B",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, bRoot))
			seed(t, s, types.Job{ID: "A", State: types.StateRunning, WritePaths: tc.holds, Checkpoint: aRev, CheckoutRoot: aRepo.Root()})
			rows, err := s.List()
			require.NoError(t, err)

			err = unorderedFileShareProblems(context.Background(), s, rows, types.Job{ID: "B", WritePaths: []string{"run.go"}, Goals: tc.goals}).Err()
			require.ErrorIs(t, err, types.WritePathFileShared)
			for _, want := range tc.want {
				assert.Contains(t, err.Error(), want)
			}
		})
	}

	t.Run("a holder that has touched nothing, and a job with no symbol goals, get the generic advice", func(t *testing.T) {
		t.Parallel()
		quiet := testkit.NewRepo(t, claimFiles)
		s := NewStore(tmpLoc(t, bRoot))
		seed(t, s, types.Job{ID: "A", State: types.StateRunning, WritePaths: []string{"run.go"}, Checkpoint: quiet.Head(), CheckoutRoot: quiet.Root()})
		rows, err := s.List()
		require.NoError(t, err)

		err = unorderedFileShareProblems(context.Background(), s, rows, types.Job{ID: "B", WritePaths: []string{"run.go"}}).Err()
		require.ErrorIs(t, err, types.WritePathFileShared)
		assert.Contains(t, err.Error(), "Claim `run.go#<declaration>` on both rows, add `--depends-on A` to B, or fold B into A")
	})
}

func TestForkNotes(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, claimFiles).Root()
	s := NewStore(tmpLoc(t, root))
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{"a claim that names a declaration", []string{"run.go#A"}, nil},
		{"the preamble", []string{"run.go#(preamble)"}, nil},
		{"a whole file", []string{"run.go"}, nil},
		{"a claim that names none", []string{"run.go#helper"}, []string{`"run.go#helper" names no declaration of run.go yet; it holds one once written, so check the spelling`}},
		{"a claim in a file not written yet", []string{"new.go#helper"}, []string{`new.go does not exist yet in this checkout, so "new.go#helper" names a declaration once it is written`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ForkNotes(ctx, s, nil, types.Job{ID: "B", WritePaths: tc.paths})
			if tc.want == nil {
				assert.Empty(t, got, "a claim that needs no check owes its reader no note")
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("a file shared with a job not yet taken", func(t *testing.T) {
		t.Parallel()
		rows := []types.Job{{ID: "A", State: types.StateDeclared, WritePaths: []string{"run.go"}}}
		goals := []types.Goal{{ID: "g", Kind: types.GoalKindSymbol, Expect: types.ExpectChanged, Symbols: []string{"C"}}}
		got := ForkNotes(ctx, s, rows, types.Job{ID: "B", WritePaths: []string{"run.go"}, Goals: goals})
		assert.Equal(t, []string{"B and A both write run.go. Claim `run.go#C` on B (its symbol goals name it) and a declaration other than that on A, or add `--depends-on A` to B, or fold B into A"}, got)
	})

	// One note per peer and file, however many of the row's entries name the file; a
	// second peer on it gets its own.
	t.Run("a file two entries and two peers share", func(t *testing.T) {
		t.Parallel()
		rows := []types.Job{
			{ID: "A", State: types.StateDeclared, WritePaths: []string{"run.go"}},
			{ID: "D", State: types.StateDeclared, WritePaths: []string{"run.go#B"}},
		}
		got := ForkNotes(ctx, s, rows, types.Job{ID: "B", WritePaths: []string{"run.go#A", "run.go#B"}})
		require.Len(t, got, 2, "%q", got)
		assert.True(t, strings.HasPrefix(got[0], "B and A both write run.go."), got[0])
		assert.True(t, strings.HasPrefix(got[1], "B and D both write run.go."), got[1])
	})
}

// A row claiming one key of a magusfile whose change already touches others is told every
// claim it lacks in one note, rather than one refusal per key.
func TestForkNotesListEveryConfigDeclarationTheChangeTouches(t *testing.T) {
	t.Parallel()

	repo := testkit.NewRepo(t, configFiles)
	rev := repo.Head()
	repo.WriteTree(map[string]string{"magusfile.buzz": strings.NewReplacer(
		`"demo"`, `"demo2"`,
		`"5m"`, `"9m"`,
		"> void {\n}", "> void {\n    ctx.log(\"x\");\n}",
	).Replace(configMagusfile)})
	s := NewStore(tmpLoc(t, repo.Root()))
	row := types.Job{ID: "B", State: types.StateRunning, WritePaths: []string{"magusfile.buzz#project.targets.test"}, Checkpoint: rev, CheckoutRoot: repo.Root()}

	got := ForkNotes(t.Context(), s, nil, row)
	assert.Equal(t, []string{"B's change touches 2 declaration(s) of magusfile.buzz that no claim of it names;" +
		" claim them all at once: `magusfile.buzz#build`, `magusfile.buzz#project.name`"}, got)

	row.WritePaths = append(row.WritePaths, "magusfile.buzz#project.name", "magusfile.buzz#build")
	assert.Empty(t, ForkNotes(t.Context(), s, nil, row), "a row claiming every declaration it touched lacks none")

	row.WritePaths, row.CheckoutRoot = []string{"magusfile.buzz#project.targets.test"}, ""
	assert.Empty(t, ForkNotes(t.Context(), s, nil, row), "a row no checkout has taken has changed nothing")
}

// strPtr returns a pointer to s, for a test table field that must tell "not given" (nil)
// apart from "given as empty".
func strPtr(s string) *string { return &s }

// TestRefuseUnorderedFileShareTransitiveDependsOn pins that depends_on orders a pair even
// through an intermediate row (C3): B depends on M, M depends on A, so B and A are ordered
// though neither names the other directly.
func TestRefuseUnorderedFileShareTransitiveDependsOn(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n"}
	bRoot := testkit.NewRepo(t, files).Root()
	aRepo := testkit.NewRepo(t, files)
	aRoot := aRepo.Root()
	aRev := aRepo.Head()

	loc := tmpLoc(t, bRoot)
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "A", State: types.StateRunning, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot})
	seed(t, s, types.Job{ID: "M", State: types.StateRunning, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot, DependsOn: []string{"A"}})
	rows, err := s.List()
	require.NoError(t, err)

	candidate := types.Job{ID: "B", WritePaths: []string{"magusfile.buzz"}, DependsOn: []string{"M"}}
	assert.Empty(t, unorderedFileShareProblems(context.Background(), s, rows, candidate))
}

func TestDescribeTouched(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "none yet", describeTouched(nil, nil))
	assert.Equal(t, "the top of the file, func A() {", describeTouched([]string{types.Preamble, "func A() {"}, nil))
	assert.Equal(t, "unknown (its footprint could not be read)", describeTouched(nil, errors.New("its footprint could not be read")))
}
