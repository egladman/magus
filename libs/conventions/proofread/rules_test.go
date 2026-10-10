package proofread

import (
	"strings"
	"testing"
)

// Each test opens with the vale styles' own .test.yml cases, carried over when
// the rules moved to Go.

var (
	blockFinding = Finding{
		Rule:    RuleCommentBlock,
		Message: "Keep a comment paragraph under 250 words: say why, and move the rest to docs.",
	}
	sentenceFinding = Finding{
		Rule:    RuleCommentSentence,
		Message: "Keep a comment sentence under 60 words: split it.",
	}
)

// wordRun is n words with no sentence end among them.
func wordRun(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) }

// sentences is n words in sentences of ten, so only a budget on the whole
// paragraph can fire.
func sentences(n int) string {
	return strings.TrimSpace(strings.Repeat("word word word word word word word word word word. ", n/10))
}

func TestCommentBlockReportsAParagraphOverTheWordBudget(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a block over 250 words",
			doc:  strings.Repeat("The cache keys a run on what it reads, so an undeclared input replays a stale verdict.\n", 17),
			want: []Finding{blockFinding},
		},
		{name: "a short block", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{
			name: "code dropped from the count",
			doc: "The cache keys a run on what it reads.\n\n" +
				"\t" + strings.Repeat("word ", 300) + "\n\n" +
				"```\n" + strings.Repeat("word ", 300) + "\n```\n",
		},
		{
			name: "paragraphs under the budget in a doc over it",
			doc:  sentences(200) + "\n\n" + sentences(200) + "\n",
		},
		{
			name: "every paragraph over the budget reports",
			doc:  sentences(260) + "\n\n" + sentences(100) + "\n\n" + sentences(260) + "\n",
			want: []Finding{blockFinding, blockFinding},
		},
		{
			name: "a list with no blank line between its items is one paragraph",
			doc:  "Lead:\n\n  - " + sentences(100) + "\n  - " + sentences(100) + "\n  - " + sentences(100) + "\n",
			want: []Finding{blockFinding},
		},
		{
			name: "a loose list is a paragraph per item",
			doc:  "Lead:\n\n  - " + sentences(100) + "\n\n  - " + sentences(100) + "\n\n  - " + sentences(100) + "\n",
		},
		{
			name: "a list is a paragraph apart from the lines before and after it",
			doc:  sentences(200) + "\n  - " + sentences(100) + "\n  - " + sentences(100) + "\n" + sentences(200) + "\n",
		},
	}

	runJudgeCases(t, cases)
}

func TestCommentSentenceReportsASentenceOverTheWordBudget(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a sentence over 60 words",
			doc: "The cache keys a run on the cache keys a run on what it reads and the cache keys a run\n" +
				"on what it reads and the cache keys a run on what it reads and the cache keys a run on\n" +
				"what it reads and the cache keys a run on what it reads and the cache keys a run on what\n" +
				"it reads and the cache keys a run on what it reads and the cache keys a run on what it\n" +
				"reads.\n",
			want: []Finding{sentenceFinding},
		},
		{name: "short sentences", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{
			name: "a paragraph break ends the count",
			doc:  strings.Repeat("word ", 40) + "\n\n" + strings.Repeat("word ", 40) + "\n",
		},
		{
			name: "a list item ends the sentence before it",
			doc:  wordRun(40) + "\n  - " + wordRun(40) + "\n  - " + wordRun(40) + "\n",
		},
		{
			name: "an unindented line after a list ends the item's sentence",
			doc:  "Lead.\n\n  - " + wordRun(40) + "\n    " + wordRun(10) + "\n" + wordRun(40) + ".\n",
		},
		{
			name: "an indented code block ends the sentence and is never counted",
			doc:  wordRun(40) + "\n\n\t" + wordRun(40) + "\n\n" + wordRun(40) + ".\n",
		},
		{
			name: "a code line with no blank around it ends the sentence",
			doc:  wordRun(40) + "\n\t" + wordRun(40) + "\n" + wordRun(40) + ".\n",
		},
		{
			name: "a fenced block ends the sentence and is never counted",
			doc:  wordRun(40) + "\n```\n" + wordRun(40) + "\n```\n" + wordRun(40) + ".\n",
		},
		{
			name: "a sentence wrapped within one list item counts whole",
			doc:  "Lead.\n\n  - " + wordRun(35) + "\n    " + wordRun(35) + ".\n  - " + wordRun(5) + ".\n",
			want: []Finding{sentenceFinding},
		},
	}

	runJudgeCases(t, cases)
}

// TestBudgetsJudgeRealTreeDocs pins the budgets against docs from this
// repository as they stood when the rules were ported. A doc that fires holds a
// genuinely long sentence or paragraph; one that does not was reported only
// while comment-block counted the whole doc.
func TestBudgetsJudgeRealTreeDocs(t *testing.T) {
	cases := []judgeCase{
		{
			name: "types/knowledge.go KnowledgeSchemaVersion: a version log with no blank lines is one long paragraph",
			doc:  knowledgeSchemaVersionDoc,
			want: []Finding{blockFinding, blockFinding, sentenceFinding, sentenceFinding},
		},
		{name: "types/doctor.go CheckFail: four paragraphs each under the budget", doc: checkFailDoc},
		{name: "vcs/git.go gitMergeDriverCommand: four paragraphs each under the budget", doc: gitMergeDriverCommandDoc},
		{
			name: "cmd/magus/job.go describeJob: one sentence over the budget",
			doc:  describeJobDoc,
			want: []Finding{sentenceFinding},
		},
		{
			name: "cmd/magus/drift.go checkDriftForCommit: one sentence over the budget",
			doc:  checkDriftForCommitDoc,
			want: []Finding{sentenceFinding},
		},
	}

	runJudgeCases(t, cases)
}

func TestFillerReportsThroatClearingAndFillerAdverbs(t *testing.T) {
	cases := []judgeCase{
		{
			name: "throat-clearing",
			doc:  "Note that the cache keys a run on what it reads.\n",
			want: []Finding{{Rule: RuleFiller, Message: "Drop 'Note that': state the fact.", Match: "Note that"}},
		},
		{
			name: "a filler adverb",
			doc:  "It simply rereads the file.\n",
			want: []Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply"}},
		},
		{name: "the fact alone", doc: "The cache keys a run on what it reads. A miss reruns it.\n"},
		{name: "a note that names the notes feature", doc: "It records a note that the queue reads. It is not this function's job.\n"},
		{name: "a backtick span is a literal", doc: "It reads the `simply` flag.\n"},
		{
			name: "a phrase wrapped across lines",
			doc:  "The cache keys a run on what it reads. It is\nimportant to declare inputs.\n",
			want: []Finding{{
				Rule:    RuleFiller,
				Message: "Drop 'It is important to': state the fact.",
				Match:   "It is important to",
			}},
		},
	}

	runJudgeCases(t, cases)
}

func TestTermsReportsTheHyphenatedSubagent(t *testing.T) {
	cases := []judgeCase{
		{
			name: "a hyphenated subagent",
			doc:  "It hands the work to a sub-agent.\n",
			want: []Finding{{Rule: RuleTerms, Message: "Write 'subagent', not 'sub-agent'.", Match: "sub-agent"}},
		},
		{name: "the one spelling", doc: "It hands the work to subagents.\n"},
		{
			name: "a capitalized plural",
			doc:  "Sub-Agents share the lease.\n",
			want: []Finding{{Rule: RuleTerms, Message: "Write 'subagents', not 'Sub-Agents'.", Match: "Sub-Agents"}},
		},
	}

	runJudgeCases(t, cases)
}

func TestNameSuffixReportsACallableNameEndingInOfOrFor(t *testing.T) {
	cases := []struct {
		name string
		want []Finding
	}{
		// A name ending in For.
		{name: "parseConfig"},
		{name: "configFor", want: []Finding{nameSuffixFinding("configFor")}},
		// A name ending in Of.
		{name: "valueOf", want: []Finding{nameSuffixFinding("valueOf")}},
		// Of and For only as whole trailing words.
		{name: "newClient"},
		{name: "ForEach"},
		{name: "Platform"},
		{name: "Offset"},
		// Snake case.
		{name: "value_of", want: []Finding{nameSuffixFinding("value_of")}},
		{name: "for"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, Judge(Symbol{Name: tc.name, Callable: true}, houseOn), tc.want)
		})
	}
}

func TestNameSuffixSkipsANameThatIsNotCallable(t *testing.T) {
	assertFindings(t, Judge(Symbol{Name: "valueOf"}, houseOn), nil)
}

func nameSuffixFinding(name string) Finding {
	return Finding{
		Rule:    RuleNameSuffix,
		Message: "Rename '" + name + "': no function or method name ends in the word Of or For.",
		Match:   name,
	}
}

const knowledgeSchemaVersionDoc = "KnowledgeSchemaVersion is stamped into every exported graph, shard, and manifest.\n" +
	"External consumers (agent skills, MCP tools, other tools reading the node-link\n" +
	"JSON) check it; a bump is a changelog event. Increment when the node/edge shape\n" +
	"or ID scheme changes in a way that would break a consumer that parsed the old form.\n" +
	"v2 added a \"command\" kind; v3 a \"tool\" kind coupled to it. v4 retires \"command\"\n" +
	"(its rendered argv was always identical to the op's static base command, so it was\n" +
	"a redundant copy of the op) and moves the model onto the op: an op carries an `argv`\n" +
	"attr and `uses` the tool (argv[0]) it runs, so `explain tool:go` reaches every op\n" +
	"that runs go and a target reaches its tool via target->op->tool. v2/v3 were unreleased.\n" +
	"v5 adds the build I/O layer: `produces`/`consumes` edges from a target's declared\n" +
	"magus.outputs/inputs to the file and doc nodes they match, so a generated file is\n" +
	"self-labeled by its producing target; plus workspace-wide authored-markdown doc nodes\n" +
	"carrying a `role` attr (readme/agent/changelog/...) and a `documents` edge to their project.\n" +
	"v6 adds the \"author\" kind: a git contributor, with `authored` edges to the files they\n" +
	"touched (the EMERGENT maintainer, to set against a file's DECLARED CODEOWNERS owner).\n" +
	"v7 changes no node or edge shape at all: it bumps because shard fingerprints are now\n" +
	"computed by streaming fields into SHA256 instead of hashing marshaled JSON, so every\n" +
	"shard's fingerprint VALUE differs from a v6 store's. The manifest check treats a\n" +
	"version mismatch as a full rebuild, which is exactly the migration needed: without\n" +
	"the bump, a v6 cache would read as current while every fingerprint disagreed, and a\n" +
	"changed shard would never be rewritten.\n" +
	"v8 adds symbol->symbol `calls` edges to the @symbols shards, attributed from the SCIP\n" +
	"occurrence's enclosing_range (the callee is referenced from inside the caller's body).\n" +
	"The relation and both node kinds already existed, so a v7 consumer parses a v8 graph\n" +
	"without changing, but it would read a symbol's edge set as complete when it is not,\n" +
	"and the shard fingerprints all differ, so the bump is what forces the rebuild.\n" +
	"v9 adds `secret_refs` to a target node: the credential references the target names,\n" +
	"alongside the `reads_secrets` flag that already recorded that it names any. The field\n" +
	"is additive, so a v8 consumer parses a v9 graph unchanged: the bump is for the OTHER\n" +
	"direction, a v8 store on disk. Its target shards were extracted before the field\n" +
	"existed, and the magusfile they were extracted from has not changed, so nothing else\n" +
	"would invalidate them: the version mismatch is what forces the rebuild that puts the\n" +
	"references there.\n" +
	"v10 adds the \"docsection\" kind: one node per markdown heading, carrying its goldmark\n" +
	"auto-heading-id anchor, so an agent retrieves the relevant section of a doc rather than\n" +
	"the whole page. A page `contains` its sections and a section `contains` its subsections.\n" +
	"The kind is additive, so a v9 consumer parses a v10 graph unchanged: the bump is for a\n" +
	"v9 store on disk, whose doc shards were extracted before headings were indexed and whose\n" +
	"source markdown has not changed, so only a version mismatch forces the rebuild that adds\n" +
	"the sections.\n" +
	"v11 makes the edge vocabulary part of the exported schema. Each relation has one\n" +
	"canonical definition (description, the labels a reader sees in either direction, and\n" +
	"the exact endpoint-kind pairs it may connect), and an export carries those definitions\n" +
	"plus a fingerprint of them, so a consumer meeting an unfamiliar predicate can look it\n" +
	"up instead of guessing, and two exports built against different vocabularies say so.\n" +
	"\n" +
	"The set is closed but not enforced at write time, deliberately: shards load lazily, so\n" +
	"an edge legitimately arrives before the node that gives its endpoint a kind, and\n" +
	"rejecting inside AddEdge would discard correct edges by arrival order.\n" +
	"Graph.UndeclaredEdges reports violations instead, and a test over this workspace's own\n" +
	"graph is what fails when a producer widens the vocabulary without declaring it.\n" +
	"v12 adds the \"link\" kind and the citation layer that mints it: a URL written in a code\n" +
	"comment, in a markdown link, or in a generated page's `generated_from` frontmatter now\n" +
	"carries an edge from the citing file or page. A citation that names something the\n" +
	"workspace already holds resolves to THAT node (a doc, a section, a file, a directory)\n" +
	"and mints no link node, so the kind covers only genuinely external documents. The kind\n" +
	"is additive and the relations are the existing `references` and `documents` with wider\n" +
	"endpoint shapes, so a v11 consumer parses a v12 graph unchanged; the bump is for a v11\n" +
	"store on disk, whose doc and buzz shards were extracted before citations were indexed\n" +
	"and whose sources have not changed, and for the relation fingerprint, which the wider\n" +
	"shapes move.\n" +
	"v13 adds a `namespace` attr to symbol nodes: the ID of the SCIP namespace symbol (a Go\n" +
	"package, a TypeScript or Python module, a Rust mod) the symbol is declared in. Additive,\n" +
	"so a v12 consumer parses a v13 graph unchanged; the bump is for a v12 store on disk,\n" +
	"whose symbol shards were extracted before the attr existed and whose indexes have not\n" +
	"changed.\n" +
	"v14 adds `signature` and `body_digest` attrs to symbol nodes: the declaration text the\n" +
	"indexer rendered and a fingerprint of the definition's lines, which is what lets a review\n" +
	"compare a symbol against its base and tell an added, removed, or re-signed API from a body\n" +
	"edit. Additive; the bump is for a v13 store whose symbol shards predate them.\n" +
	"v15 adds `lines` and `bytes` attrs to file nodes an extractor read (buzz sources, and the\n" +
	"defining files of a SCIP index), so a reader can size a file before paging through it.\n" +
	"Additive; the bump is for a v14 store whose buzz and symbol shards predate them.\n" +
	"v16 adds `marker` nodes with `declared` edges, dir -imports-> dir and dir -calls-> dir\n" +
	"edges, edge attrs, and the `layer` and `language` attrs on dir nodes. The bump is for a\n" +
	"v15 store whose shards predate them."

const checkFailDoc = "CheckFail and CheckAdvice are a deliberate split, and which one a check returns\n" +
	"is a statement about whose judgment is involved.\n" +
	"\n" +
	"CheckFail is for a workspace that is WRONG in a way nobody's taste can rescue:\n" +
	"a dependency cycle, a magusfile that will not parse, two targets claiming one\n" +
	"output, a policy naming a target that does not exist. These are facts, they\n" +
	"break the build or corrupt the cache, and failing on them is not an opinion.\n" +
	"\n" +
	"CheckAdvice is for a convention magus RECOMMENDS: how targets are named,\n" +
	"whether every project binds a language spell, whether a spell target carries a\n" +
	"doc comment. These are conventions that have worked well, documented so you can\n" +
	"take them, not requirements, because magus does not get to decide how your\n" +
	"repository is laid out. `ci` is the one reserved target, and everything past it\n" +
	"is yours.\n" +
	"\n" +
	"The distinction is not cosmetic. When doctor had only ok and fail, a convention\n" +
	"check had two options: fail (and dictate) or not exist. What actually happened\n" +
	"is that each one grew its own private escape hatch (no_language for language\n" +
	"coverage, and briefly allow_bespoke_name for target naming), so the config\n" +
	"file grew one key per opinion, and taking magus's advice became mandatory\n" +
	"unless you wrote a paragraph explaining yourself. Advice that exits zero needs\n" +
	"no escape hatch at all.\n" +
	"\n" +
	"There is deliberately no switch that promotes advice to failure. A knob for\n" +
	"that would just be the imposition again with an opt-in label on it, and the\n" +
	"workspace that wants a convention enforced can enforce it: in its own lint\n" +
	"target, with its own tools, on its own terms. magus reports what it noticed and\n" +
	"gets out of the way."

const gitMergeDriverCommandDoc = "gitMergeDriverCommand is the command line git runs to resolve a conflict in a\n" +
	"generated file. Registering the bare word \"magus\" assumes a released binary on\n" +
	"PATH; when there is not one, git cannot execute the driver and silently falls\n" +
	"back to conflict markers in every generated file, which is indistinguishable\n" +
	"from having no driver at all. Prefer PATH so the registration survives an\n" +
	"upgrade-in-place, and fall back to this binary's own absolute path so a\n" +
	"source checkout with no installed magus still merges cleanly.\n" +
	"\n" +
	"PATH is preferred only if that binary answers the spelling being registered. Existing on\n" +
	"PATH is not enough: a source checkout finds an INSTALLED RELEASE there, and pairing that\n" +
	"path with this binary's spelling registers something nothing can dispatch. One release plus\n" +
	"one source tree is enough to hit it: the ordinary development setup.\n" +
	"\n" +
	"A magus built at the workspace root comes FIRST, ahead of PATH. Answering the spelling is\n" +
	"a weaker test than it looks: driverExeAnswers probes with -h, which returns before the\n" +
	"child opens a workspace, so a release too old to READ this magusfile still answers and\n" +
	"still wins. That is how one v0.3.0 build became the registered driver for 142 worktrees\n" +
	"of the repo that defines magus, each failing at load on every conflict. A binary in the\n" +
	"tree is the one that can read the tree.\n" +
	"\n" +
	"It reaches only a workspace that builds an executable named `magus` at its root, which in\n" +
	"practice is magus's own. Everyone else keeps the PATH registration and its\n" +
	"upgrade-in-place behavior."

const describeJobDoc = "describeJob is `magus describe job`: one job's terms, which is what a holder reads on\n" +
	"arrival. It prints the criteria, the write paths, the check, the model, the checkpoint, the\n" +
	"dependencies, what the workspace itself puts out of reach, the graph's blast radius for\n" +
	"each write path, and where each declared goal stands graded against the evidence magus\n" +
	"holds now, and it prints no procedure: taking the job is `magus job exec`'s work to DO,\n" +
	"not a paragraph for somebody to follow by hand."

const checkDriftForCommitDoc = "checkDriftForCommit is the VCS-facing half of serverCheckDrift, kept separate so it can\n" +
	"be exercised against a real repository without needing a full magus workspace:\n" +
	"classify and gofmtList are the two things that need one (turning changed paths into\n" +
	"their declared source/output role, and asking a real gofmt binary about a file), and a\n" +
	"test supplies its own instead of loading a workspace or shelling a real tool.\n" +
	"\n" +
	"It returns ok=false, with no error, for every ordinary reason there is nothing to say:\n" +
	"no parent commit (the repository's first commit), nothing changed, or nothing that\n" +
	"changed lands in either class."
