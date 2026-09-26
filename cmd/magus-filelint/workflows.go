package main

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	setupMagusAction = ".github/actions/setup-magus/action.yml"
	queueApplyFlow   = ".github/workflows/queue-apply.yaml"
	appTokenAction   = "actions/create-github-app-token@"
)

// actionStep is the part of a workflow or composite-action step these rules read.
type actionStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
	Env  map[string]string `yaml:"env"`
	line int
}

func (s *actionStep) UnmarshalYAML(n *yaml.Node) error {
	type plain actionStep
	if err := n.Decode((*plain)(s)); err != nil {
		return err
	}
	s.line = n.Line
	return nil
}

// label names a step the way a reader finds it in the file.
func (s actionStep) label() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Uses
}

// setupMagusMintsTheQueueAppTokenOnlyAsAnOutput: setup-magus mints the merge
// queue's app token, and the rules around it are ones no run exercises until a
// repository adds the app. A key is never an action input (the calling step puts
// it in env, where it is masked and scoped to that step), half an app fails the
// job before anything installs, the minting action is pinned by commit, and the
// token leaves only as an output, never through $GITHUB_ENV, since this action
// also runs in jobs that execute pull-request code.
func setupMagusMintsTheQueueAppTokenOnlyAsAnOutput(fsys fs.FS) []finding {
	raw, bad := readFile(fsys, setupMagusAction)
	if bad != nil {
		return bad
	}
	var findings []finding
	flag := func(p string, line int, problem, fix string) {
		findings = append(findings, finding{path: p, line: line, problem: problem, fix: fix})
	}

	var action struct {
		Inputs map[string]struct {
			Default string `yaml:"default"`
		} `yaml:"inputs"`
		Outputs map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"outputs"`
		Runs struct {
			Steps []actionStep `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &action); err != nil {
		return []finding{{path: setupMagusAction, problem: "does not parse: " + err.Error()}}
	}

	credential := regexp.MustCompile(`(?i)key|token|secret|password`)
	for name := range action.Inputs {
		if credential.MatchString(name) {
			flag(setupMagusAction, lineOf(raw, "\n  "+name+":"), "input "+name+" carries a credential",
				"A credential reaches setup-magus through the calling step's env, never an input.")
		}
	}
	if id, ok := action.Inputs["queue-app-client-id"]; !ok {
		flag(setupMagusAction, 0, "declares no queue-app-client-id input", "The queue app's client id is an input.")
	} else if id.Default != "" {
		flag(setupMagusAction, lineOf(raw, "queue-app-client-id:"), "queue-app-client-id has a default",
			"No app unless the caller names one: drop the default.")
	}

	steps := action.Runs.Steps
	if len(steps) == 0 {
		return append(findings, finding{path: setupMagusAction, problem: "runs no steps"})
	}
	const halfApp = "(inputs.queue-app-client-id == '') != (env.MAGUS_QUEUE_APP_PRIVATE_KEY == '')"
	if steps[0].If != halfApp || !strings.Contains(steps[0].Run, "exit 1") {
		flag(setupMagusAction, steps[0].line, "the first step does not refuse half an app",
			"Half an app is refused first, before anything installs: the first step runs `exit 1` if: "+halfApp)
	}

	var mint *actionStep
	for i := range steps {
		if strings.HasPrefix(steps[i].Uses, appTokenAction) {
			mint = &steps[i]
		}
		if strings.Contains(steps[i].Run, "GITHUB_ENV") && strings.Contains(steps[i].Run, "queue") {
			flag(setupMagusAction, steps[i].line, fmt.Sprintf("step %q writes the queue app's credential to $GITHUB_ENV", steps[i].Name),
				"The token leaves only as an action output; this action runs in jobs that execute pull-request code.")
		}
	}
	if mint == nil {
		flag(setupMagusAction, 0, "setup-magus does not mint the queue app's token", "Mint it with "+appTokenAction+"<commit>.")
	} else {
		if !regexp.MustCompile(`@[0-9a-f]{40}$`).MatchString(mint.Uses) {
			flag(setupMagusAction, mint.line, mint.Uses+" is not pinned by full commit", "Pin the minting action to a 40-hex commit.")
		}
		want := map[string]string{
			"if":           "inputs.queue-app-client-id != ''",
			"private-key":  "${{ env.MAGUS_QUEUE_APP_PRIVATE_KEY }}",
			"repositories": "${{ github.event.repository.name }}",
			// Apply follows the validation run and dispatches nothing; the dispatch
			// job mints its own actions: write token.
			"permission-contents":      "write",
			"permission-pull-requests": "write",
			"permission-statuses":      "write",
			"permission-actions":       "read",
			"permission-workflows":     "write",
		}
		for _, key := range slices.Sorted(maps.Keys(want)) {
			got := mint.With[key]
			if key == "if" {
				got = mint.If
			}
			if got != want[key] {
				flag(setupMagusAction, mint.line, fmt.Sprintf("the minting step's %s is %q", key, got),
					fmt.Sprintf("Set it to %q: the token is this repository's alone, with exactly the permissions the queue uses.", want[key]))
			}
		}
	}
	for _, out := range []string{"queue-token", "queue-committer", "queue-app-slug"} {
		if _, ok := action.Outputs[out]; !ok {
			flag(setupMagusAction, 0, "declares no "+out+" output", "The queue app's token and identity leave as outputs.")
		}
	}

	return append(findings, queueApplyHoldsTheAppToken(fsys)...)
}

// queueApplyHoldsTheAppToken is the calling side of the setup-magus rule: the
// key is released to main's runs alone, and every write the queue makes is the
// app's, since a run or a merge the job's own token makes starts no workflow.
func queueApplyHoldsTheAppToken(fsys fs.FS) []finding {
	raw, bad := readFile(fsys, queueApplyFlow)
	if bad != nil {
		return bad
	}
	var findings []finding
	flag := func(line int, problem, fix string) {
		findings = append(findings, finding{path: queueApplyFlow, line: line, problem: problem, fix: fix})
	}
	var workflow struct {
		Jobs map[string]struct {
			Environment string       `yaml:"environment"`
			Steps       []actionStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(raw), &workflow); err != nil {
		return []finding{{path: queueApplyFlow, problem: "does not parse: " + err.Error()}}
	}
	const envFix = "Set `environment: magus-queue`: the key is released to main's runs alone."
	const key = "${{ secrets.MAGUS_QUEUE_APP_PRIVATE_KEY }}"
	const clientID = "${{ vars.MAGUS_QUEUE_APP_CLIENT_ID }}"

	apply := workflow.Jobs["apply"]
	if apply.Environment != "magus-queue" {
		flag(lineOf(raw, "\n  apply:"), "job apply runs outside the magus-queue environment", envFix)
	}
	var setup *actionStep
	for i := range apply.Steps {
		if apply.Steps[i].Uses == "./.github/actions/setup-magus" {
			setup = &apply.Steps[i]
		}
	}
	if setup == nil {
		flag(lineOf(raw, "\n  apply:"), "job apply does not run setup-magus", "apply gets the queue app's token from setup-magus.")
	} else {
		if setup.Env["MAGUS_QUEUE_APP_PRIVATE_KEY"] != key {
			flag(setup.line, "setup-magus's env.MAGUS_QUEUE_APP_PRIVATE_KEY is not "+key, "Pass the key through the step's env.")
		}
		if setup.With["queue-app-client-id"] != clientID {
			flag(setup.line, "setup-magus's queue-app-client-id is not "+clientID, "Pass the client id as the input.")
		}
	}
	for _, banned := range [][2]string{
		{"github.token", "A workflow names the token as secrets.GITHUB_TOKEN."},
		{"GH_TOKEN", "gh reads GITHUB_TOKEN."},
		{"|| secrets.GITHUB_TOKEN", "The queue writes only as its app: a run or a merge the job's own token makes starts no workflow."},
	} {
		if strings.Contains(raw, banned[0]) {
			flag(lineOf(raw, banned[0]), "names "+banned[0], banned[1])
		}
	}

	// A run the Actions token dispatches fires no workflow_run, so no apply would
	// follow it: dispatch holds the app's token, minted for dispatching alone.
	dispatch := workflow.Jobs["dispatch"]
	if dispatch.Environment != "magus-queue" {
		flag(lineOf(raw, "\n  dispatch:"), "job dispatch runs outside the magus-queue environment", envFix)
	}
	var mint *actionStep
	for i := range dispatch.Steps {
		if strings.HasPrefix(dispatch.Steps[i].Uses, appTokenAction) {
			mint = &dispatch.Steps[i]
		}
	}
	if mint == nil {
		flag(lineOf(raw, "\n  dispatch:"), "job dispatch does not mint the queue app's token",
			"A run the Actions token dispatches fires no workflow_run; mint the app's token with "+appTokenAction+"<commit>.")
		return findings
	}
	for _, want := range [][2]string{{"private-key", key}, {"client-id", clientID}, {"permission-actions", "write"}} {
		if got := mint.With[want[0]]; got != want[1] {
			flag(mint.line, fmt.Sprintf("dispatch's minting step has %s %q", want[0], got), fmt.Sprintf("Set it to %q.", want[1]))
		}
	}
	if _, ok := mint.With["permission-contents"]; ok {
		flag(mint.line, "dispatch's minting step asks for permission-contents", "Dispatching needs actions alone; drop it.")
	}
	return findings
}

// restoreSwitch is the input that turns an action's cache restore off, and
// whether off is its default.
type restoreSwitch struct {
	input        string
	offByDefault bool
}

// restoreSwitches are the actions that restore an Actions cache unless told not to.
var restoreSwitches = map[string]restoreSwitch{
	"jdx/mise-action@":              {input: "cache"},
	"./.github/actions/setup-magus": {input: "restore-history", offByDefault: true},
	"docker/setup-qemu-action@":     {input: "cache-image"},
	"docker/setup-buildx-action@":   {input: "cache-binary"},
	"msys2/setup-msys2@":            {input: "cache"},
	"actions/setup-go@":             {input: "cache"},
}

// alwaysUntrustedFlows restore nothing whatever they hold: their verdicts decide merges.
var alwaysUntrustedFlows = map[string]string{
	"queue.yaml": "validates pull-request code, and its verdicts decide merges",
}

// cacheRestoreExemptions are trusted jobs allowed to restore, keyed
// <workflow file>/<job>, each with its reason. An exemption that no longer
// matches a restoring trusted job is itself a finding.
//
// TODO: queue-apply.yaml belongs to another change; delete this entry once its
// mise-action carries cache: false.
var cacheRestoreExemptions = map[string]string{
	"queue-apply.yaml/apply": "jdx/mise-action restores main's scope beside the queue's write token",
}

// trustedJobsRestoreNoActionsCache: a job holding a secret, a write token or
// id-token restores no Actions cache. main's cache scope is writable by any
// main-scoped run, and the merge queue validates pull-request code in one; a
// hook that escapes can read the runtime token and plant an entry every branch
// and tag run restores. See docs/concepts/merge-queue.md, Trust model.
func trustedJobsRestoreNoActionsCache(fsys fs.FS) []finding {
	// Restores only on a pull request, where a secret gated off pull requests is absent.
	const prOnly = "${{ github.event_name == 'pull_request' }}"
	const prOnlyIf = "github.event_name == 'pull_request'"
	const offPR = "github.event_name != 'pull_request'"

	paths, err := fs.Glob(fsys, ".github/workflows/*.yaml")
	if err != nil || len(paths) == 0 {
		return []finding{{path: ".github/workflows", problem: "holds no *.yaml workflow", fix: "The rule reads every workflow; restore them or move the rule."}}
	}
	var findings []finding
	used := map[string]bool{}
	for _, p := range paths {
		raw, bad := readFile(fsys, p)
		if bad != nil {
			findings = append(findings, bad...)
			continue
		}
		var wf struct {
			Permissions yaml.Node            `yaml:"permissions"`
			Env         yaml.Node            `yaml:"env"`
			Jobs        map[string]yaml.Node `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(raw), &wf); err != nil {
			findings = append(findings, finding{path: p, problem: "does not parse: " + err.Error()})
			continue
		}
		file := path.Base(p)
		for _, name := range slices.Sorted(maps.Keys(wf.Jobs)) {
			node := wf.Jobs[name]
			var job struct {
				Permissions yaml.Node    `yaml:"permissions"`
				Steps       []actionStep `yaml:"steps"`
			}
			if err := node.Decode(&job); err != nil {
				findings = append(findings, finding{path: p, line: node.Line, problem: "job " + name + " does not parse: " + err.Error()})
				continue
			}
			perms := &job.Permissions
			if perms.Kind == 0 {
				perms = &wf.Permissions
			}
			found, gated := namesSecrets(offPR, &node, &wf.Env)
			_, untrusted := alwaysUntrustedFlows[file]
			always := untrusted || grantsWrite(perms) || (found && !gated)
			// Trusted off pull requests only: a secret every mention of which is gated.
			prAllowed := !always && found
			if !always && !found {
				continue
			}

			type violation struct {
				step actionStep
				what string
			}
			var violations []violation
			for _, step := range job.Steps {
				if strings.HasPrefix(step.Uses, "actions/cache@") || strings.HasPrefix(step.Uses, "actions/cache/restore@") {
					if prGated := prAllowed && step.If == prOnlyIf; !prGated {
						violations = append(violations, violation{step, step.Uses})
					}
					continue
				}
				for prefix, sw := range restoreSwitches {
					if !strings.HasPrefix(step.Uses, prefix) {
						continue
					}
					v, set := step.With[sw.input]
					restores := !sw.offByDefault
					if set {
						restores = v != "false"
					}
					if prGated := prAllowed && v == prOnly; restores && !prGated {
						violations = append(violations, violation{step, fmt.Sprintf("%s (%s: %q)", step.Uses, sw.input, v)})
					}
				}
			}

			key := file + "/" + name
			if _, ok := cacheRestoreExemptions[key]; ok {
				used[key] = true
				if len(violations) == 0 {
					findings = append(findings, finding{path: p, line: node.Line,
						problem: key + " no longer restores a cache", fix: "Delete its entry from cacheRestoreExemptions."})
				}
				continue
			}
			for _, v := range violations {
				findings = append(findings, finding{path: p, line: v.step.line,
					problem: key + " holds a secret, a write token or id-token (or is the queue), and restores an Actions cache: " + v.what,
					fix:     "Turn the restore off (mise-action cache: false, setup-magus restore-history unset)."})
			}
		}
	}
	for _, key := range slices.Sorted(maps.Keys(cacheRestoreExemptions)) {
		if !used[key] {
			findings = append(findings, finding{path: ".github/workflows", problem: "exemption " + key + " names no trusted job",
				fix: "Delete it from cacheRestoreExemptions."})
		}
	}
	return findings
}

// grantsWrite reports whether a permissions block can write. Undeclared means
// the repository default, which can be write.
func grantsWrite(n *yaml.Node) bool {
	switch n.Kind {
	case 0:
		return true
	case yaml.ScalarNode:
		return n.Value == "write-all"
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			if n.Content[i].Value == "write" {
				return true
			}
		}
	}
	return false
}

var (
	exprRe   = regexp.MustCompile(`\$\{\{(.*?)\}\}`)
	secretRe = regexp.MustCompile(`secrets\.(\w+)`)
)

// namesSecrets reports whether the nodes name a secret other than GITHUB_TOKEN,
// and whether every such mention sits in an expression carrying offPR, which is
// empty on a pull request.
func namesSecrets(offPR string, nodes ...*yaml.Node) (found, gated bool) {
	gated = true
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			for _, m := range exprRe.FindAllStringSubmatch(n.Value, -1) {
				for _, s := range secretRe.FindAllStringSubmatch(m[1], -1) {
					if s[1] == "GITHUB_TOKEN" {
						continue
					}
					found = true
					if !strings.Contains(m[1], offPR) {
						gated = false
					}
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return found, gated
}
