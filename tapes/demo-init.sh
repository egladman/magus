#!/usr/bin/env bash
# demo-init.sh materializes the throwaway workspace the VHS tapes record
# against, and is meant to be SOURCED, not executed: it has to leave the
# recording shell sitting inside the workspace it just built.
#
#   source tapes/demo-init.sh
#
# Why a generated workspace rather than a fixture committed under tapes/:
# magus discovers a project from any magusfile.buzz below the workspace root,
# and there is no ignore mechanism. A demo magusfile checked in here would be
# picked up as a real project of the magus repo - the same failure a stale
# .claude/worktrees copy causes (MGS1002). So the fixture is written out at
# record time, outside the tree, and never exists inside it.
#
# The temp dir is also what makes the cache story honest. magus keeps its cache
# in .magus/ under the workspace root, so a fresh directory is a genuinely cold
# cache: the first `magus run build` really does the work and the second really
# replays it. Nothing here clears or touches the developer's own cache.

# Three small Go projects, stdlib only. Independent modules rather than one
# module with cross-project replace directives: the point on screen is the
# cold-then-warm cache, and inter-module wiring would cost demo lines without
# adding to it.
_demo_projects=(libs/greeter libs/mathx apps/hello)

_demo_root=$(mktemp -d "${TMPDIR:-/tmp}/magus-demo.XXXXXX")

# The tapes drive the magus built from this checkout, not whatever release
# happens to be on PATH, so a recording shows the behavior of the commit it was
# recorded at. BASH_SOURCE[0] resolves relative to this file, so sourcing works
# from any cwd.
_demo_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PATH="$_demo_repo:$PATH"
export PATH

mkdir -p "$_demo_root"
for _demo_p in "${_demo_projects[@]}"; do
	mkdir -p "$_demo_root/$_demo_p"
done

cat >"$_demo_root/magus.yaml" <<'YAML'
default_charms:
  - rw
YAML

# Root project: an aggregator whose targets fan out to the three below, so a
# bare `magus run build` from the root means "build everything".
cat >"$_demo_root/magusfile.buzz" <<'BUZZ'
import "magus";

// No "spells" list: this project binds no toolchain, and the magusfile driver
// that runs the targets below is bound by magus for every project (MGS1017).
magus\project({
    "name": "demo",
});

export fun preflight(ctx: magus\Context, args: [str]) > void {}
export fun build(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun test(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun lint(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build, test); }
BUZZ

for _demo_p in "${_demo_projects[@]}"; do
	cat >"$_demo_root/$_demo_p/magusfile.buzz" <<'BUZZ'
import "magus";
import "magus/spell/go";

magus\project({
    "spells": [go],
});

export fun preflight(ctx: magus\Context, args: [str]) > void {}
export fun lint(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-vet"](ctx); }
export fun build(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-build"](ctx); }
export fun test(ctx: magus\Context, args: [str]) > void { ctx.needs(preflight); go["go-test"](ctx); }
export fun ci(ctx: magus\Context, args: [str]) > void { ctx.needs(lint, build, test); }
BUZZ

	cat >"$_demo_root/$_demo_p/go.mod" <<BUZZ
module demo/$(basename "$_demo_p")

go 1.25
BUZZ
done

cat >"$_demo_root/libs/greeter/greeter.go" <<'GO'
// Package greeter renders friendly greetings.
package greeter

import "fmt"

// Greet returns a greeting addressed to name.
func Greet(name string) string {
	return fmt.Sprintf("hello, %s", name)
}
GO

cat >"$_demo_root/libs/greeter/greeter_test.go" <<'GO'
package greeter

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("magus"); got != "hello, magus" {
		t.Fatalf("Greet(magus) = %q", got)
	}
}
GO

cat >"$_demo_root/libs/mathx/mathx.go" <<'GO'
// Package mathx holds small numeric helpers.
package mathx

// Sum adds every value in xs.
func Sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}
GO

cat >"$_demo_root/libs/mathx/mathx_test.go" <<'GO'
package mathx

import "testing"

func TestSum(t *testing.T) {
	if got := Sum([]int{1, 2, 3}); got != 6 {
		t.Fatalf("Sum([1 2 3]) = %d", got)
	}
}
GO

cat >"$_demo_root/apps/hello/main.go" <<'GO'
// Command hello prints a greeting.
package main

import "fmt"

func main() {
	fmt.Println("hello from the demo workspace")
}
GO

# A repo, because affected tracking and the drift gates read VCS state. Paths
# are named explicitly rather than `git add -A`: the magus guard denies the
# sweep form, and this way the commit is exactly the fixture.
git -C "$_demo_root" init -q .
git -C "$_demo_root" add -- magus.yaml magusfile.buzz libs apps
git -C "$_demo_root" \
	-c user.email=demo@example.com \
	-c user.name=magus \
	commit -qm "demo workspace"

cd "$_demo_root" || return 1

# A short, stable prompt. The recording is 900px wide and a real prompt would
# spend most of it on a path nobody can read anyway.
PS1='$ '
export PS1

unset _demo_p _demo_projects _demo_root _demo_repo
