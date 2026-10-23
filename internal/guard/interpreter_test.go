package guard

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/testkit"
)

// TestInterpreterScriptDeniesAdHocScripts: Buzz is the scripting language, so a program
// typed for python, node, perl or ruby, or piped into one, is refused; a version or help
// query, a module, a program file in node, perl or ruby, and every shell pass. The
// substitute-and-write shapes scripted-rewrite was measured on stay refused here.
func TestInterpreterScriptDeniesAdHocScripts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		command string
		denied  bool
	}{
		{command: `python3 -c "print(1)"`, denied: true},
		{command: `python -c 'import sys; print(sys.path)'`, denied: true},
		{command: `python3.12 -c x`, denied: true},
		{command: `python3 -Bc 'print(1)'`, denied: true},
		{command: `python3 -W ignore -c 'print(1)'`, denied: true},
		{command: "python3 - <<'PY'\nprint(1)\nPY", denied: true},
		{command: "python3 <<'PY'\nprint(1)\nPY", denied: true},
		{command: `echo '{}' | python3`, denied: true},
		{command: `python3 hack/x.py --flag`, denied: true},
		{command: `python3 -u p3.py`, denied: true},
		{command: `./tool.py --flag`, denied: true},
		{command: `env FOO=1 python3 -c x`, denied: true},
		{command: `bash -c "python3 -c 'print(1)'"`, denied: true},
		{command: `node -e "console.log(1)"`, denied: true},
		{command: `node -p 1+1`, denied: true},
		{command: `node --eval=x`, denied: true},
		{command: "node - <<'JS'\nconsole.log(1)\nJS", denied: true},
		{command: `echo x | node`, denied: true},
		{command: `perl -pi -e 's/a/b/' f.go`, denied: true},
		{command: `perl -i.bak -pe s/a/b/ f`, denied: true},
		{command: `perl -ne 'print if /a/' f.go`, denied: true},
		{command: `perl -E 'say 1'`, denied: true},
		{command: "perl <<'PL'\nprint 1;\nPL", denied: true},
		{command: `ruby -e 'puts 1'`, denied: true},
		{command: `ruby -i -pe 'gsub(/a/,"b")' f.rb`, denied: true},
		{command: "ruby - <<'RB'\nputs 1\nRB", denied: true},
		{command: `python3 -c "import io,re; s=io.open('f.go').read(); s=re.sub(r'\bA\b','B',s); io.open('f.go','w').write(s)"`, denied: true},
		{command: "python3 - <<'PY'\ns=re.subn(r'\\bP50\\b','P50Seconds',s)\nio.open(p,'w').write(s)\nPY", denied: true},
		{command: "python3 - >> out.txt <<'PY'\nimport sys\nsys.stdout.write(open('f.go').read())\nPY", denied: true},
		// A scratch path is no exception: the language is what is refused, not the write.
		{command: "python3 - <<'PY'\nopen('/tmp/x/plan.md', 'w').write(s.replace('a', 'b'))\nPY", denied: true},
		// An unparsable line is read by the pattern.
		{command: `python3 -c "s=s.replace('a','b'); open(p,'w').write(s)" 'unterminated`, denied: true},

		{command: `python3 --version`},
		{command: `python -V`},
		{command: `python3 --help`},
		{command: `python3 -m http.server`},
		{command: `node --version`},
		{command: `node build.js`},
		{command: `node --test`},
		{command: `node -r ./setup.js build.js`},
		{command: `perl -v`},
		{command: `perl tools/x.pl`},
		{command: `perl -Iinclude tools/x.pl`},
		{command: `ruby -v`},
		{command: `ruby -Ilib script.rb`},
		{command: `bash -c 'echo hi'`},
		{command: `sh -c 'echo hi'`},
		{command: `magus buzz -e 'std\print("hi");'`},
		// A heredoc into cat or tee is text, whatever program it documents.
		{command: "cat >> notes.md <<'EOF'\npython3 -c 'print(1)'\nEOF"},
		{command: "tee -a notes.md <<'EOF'\nperl -pi -e 's/a/b/' f.go\nEOF"},
		{command: "cat >> notes.md <<EOF\nrun python3 with re.sub( then .write( and $(\nEOF"},
		{command: `echo 'python3 -c x'`},
	} {
		v := Evaluate(strict(testDependencies()), tt.command)
		if !tt.denied {
			assert.NotEqual(t, denyRuleInterpreterScript, v.Rule.Name, tt.command)
			continue
		}
		require.Equal(t, denyRuleInterpreterScript, v.Rule.Name, tt.command)
		assert.Contains(t, v.Deny, "buzz -e '<buzz>'", tt.command)
		assert.Contains(t, v.Deny, "magus-buzz-lang", tt.command)
	}
}

// TestInterpreterScriptJudgesAScriptsProgram: a node, perl or ruby file passes the line
// rule, so its program is read, and a write leaving a substitute-and-write gets the same
// verdict. A program whose every named path lies outside the workspace is untouched.
func TestInterpreterScriptJudgesAScriptsProgram(t *testing.T) {
	w := testkit.NewWorkspace(t)
	rewrite := "const fs = require('fs');\nconst s = fs.readFileSync('internal/x.go', 'utf8').replace(/A/g, 'B');\nfs.createWriteStream('internal/x.go').write(s);\n"
	scratch := "const fs = require('fs');\nconst s = fs.readFileSync('/tmp/x/notes.md', 'utf8').replace(/A/g, 'B');\nfs.createWriteStream('/tmp/x/notes.md').write(s);\n"
	for name, body := range map[string]string{"p3.py": scriptFixtures["p3.py"], "rw.js": rewrite, "scratch.js": scratch} {
		w.WriteExecutable(name, body)
	}
	dir := w.Root()
	for _, tt := range []struct {
		command string
		denied  bool
	}{
		{"python3 p3.py", true},
		{"python3 -u p3.py", true},
		{"node rw.js", true},
		{"node scratch.js", false},
	} {
		v := denyScriptContent(strict(Dependencies{}), dir, tt.command, DialectBash)
		if !tt.denied {
			assert.Empty(t, v.Deny, tt.command)
			continue
		}
		assert.Equal(t, denyRuleInterpreterScript, v.Rule.Name, tt.command)
		assert.Contains(t, v.Why, "Judged from the content of", tt.command)
	}

	written := denyScriptWrite(strict(Dependencies{}), dir+"/new.js", writeFields{Content: rewrite})
	assert.Equal(t, denyRuleInterpreterScript, written.Rule.Name)
	assert.Empty(t, denyScriptWrite(strict(Dependencies{}), dir+"/s.js", writeFields{Content: scratch}).Deny)
}

// TestGuardJudgesWhatABuzzProgramSpawns: `magus shell 'go test ./...'` is refused as a raw
// tool, so the same argv through proc\exec in a Buzz body is judged as that command too,
// every rule included. Only literal argv is read; a computed one proves nothing.
func TestGuardJudgesWhatABuzzProgramSpawns(t *testing.T) {
	t.Parallel()
	buzz := func(program string) string { return "magus buzz -e '" + program + "'" }
	for _, tt := range []struct {
		command string
		rule    denyRuleName // "" for no deny
	}{
		{buzz(`proc\exec("go", args: ["test", "./..."]);`), denyRuleRawTool},
		{buzz(`proc\exec("go", ["test", "./..."], ".", {});`), denyRuleRawTool},
		{buzz(`final r = proc\exec("go", args: ["test", "./..."], opts: {"quiet": true});`), denyRuleRawTool},
		{buzz(`proc\shell("go test ./...");`), denyRuleRawTool},
		{buzz(`proc\exec("sh", args: ["-c", "go test ./..."]);`), denyRuleRawTool},
		{buzz(`proc\exec("python3", args: ["-c", "print(1)"]);`), denyRuleInterpreterScript},
		{"magus buzz - <<'EOF'\nimport \"proc\";\nproc\\exec(\"go\", args: [\"test\", \"./...\"]);\nEOF", denyRuleRawTool},

		{buzz(`proc\exec("git", args: ["status"]);`), ""},
		// The literal spawns of hack/dev/prune-worktrees.buzz, a remedy the guard serves.
		{buzz(`proc\exec("go", args: ["env", "GOCACHE"]); proc\exec("lsof", args: ["-d", "cwd", "-Fpn", "-w"]);`), ""},
		{buzz(`proc\exec(bin, args: ["test", "./..."]);`), ""},
		{buzz(`proc\exec("go", args: ["test"] + extra);`), ""},
		{buzz(`proc\exec("go", args: ["test", "{pkg}"]);`), ""},
		{"magus buzz --check -e '" + `proc\exec("go", args: ["test", "./..."]);` + "'", ""},
	} {
		v := Evaluate(strict(testDependencies()), tt.command)
		if tt.rule == "" {
			assert.Empty(t, v.Deny, tt.command)
			continue
		}
		require.Equal(t, tt.rule, v.Rule.Name, tt.command)
		assert.Contains(t, v.Why, "Judged as the command the Buzz program starts", tt.command)
	}

	dir := testkit.NewWorkspace(t)
	dir.WriteExecutable("spawn.buzz", "import \"proc\";\nexport fun main(args: [str]) > void !> any {\n    proc\\exec(\"go\", args: [\"test\", \"./...\"]);\n}\n")
	dir.WriteExecutable("status.buzz", "import \"proc\";\nexport fun main(args: [str]) > void !> any {\n    proc\\exec(\"git\", args: [\"status\"]);\n}\n")
	v := denyScriptContent(strict(testDependencies()), dir.Root(), "magus buzz spawn.buzz", DialectBash)
	assert.Equal(t, denyRuleRawTool, v.Rule.Name)
	assert.Contains(t, v.Why, "Judged from the content of spawn.buzz")
	assert.Empty(t, denyScriptContent(strict(testDependencies()), dir.Root(), "magus buzz status.buzz", DialectBash).Deny)
}

func TestBuzzSpawnsSpellsLiteralArgv(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		program string
		want    []string
	}{
		{`proc\exec("go", args: ["test", "./..."]);`, []string{"go test ./..."}},
		{`proc\exec("echo", args: ["two words", "it's"]);`, []string{fmt.Sprintf("echo %s %s", `'two words'`, `"it's"`)}},
		{`proc\exec("gh", args: ["api", "x"]); proc\shell("ls -la");`, []string{"gh api x", "ls -la"}},
		{`proc\exec("pnpm", ["exec", "biome"], ".", {});`, []string{"pnpm exec biome"}},
		{`proc\exec("printf", args: ["a\"b"]);`, []string{`printf 'a"b'`}},
		{`proc\exec(cmd, args: args);`, nil},
		{`proc\exec("go", args: words);`, nil},
		{`proc\exec("echo", args: ["{name}"]);`, nil},
	} {
		assert.Equal(t, tt.want, buzzSpawns(tt.program), tt.program)
	}
}
