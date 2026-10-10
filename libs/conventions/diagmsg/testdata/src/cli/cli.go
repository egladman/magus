package cli

import (
	"errors"
	"fmt"

	"diag"
	"guard"
)

const stale = "graph is stale: run `magus graph build`"

const tagged = "server: " + "not running"

func calls(name string) {
	_ = diag.Errorf(1, "target %q not found: run `magus ls targets .`", name)
	_ = diag.Errorf(1, stale)
	_ = diag.Errorf(1, "target %q is unknown to this workspace and to every project it declares, %s", name, name) // want `message-length: Keep a message to 60 runes \(this one has 73\): state the verdict`
	_ = diag.Errorf(1, "%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v%v")
	_ = diag.Errorf(1, "refused because the row ended, so nothing runs") // want `message-rationale: Give a message one reason \(this one joins 2\)`
	_ = diag.Errorf(1, "run `magus graph build`, then `magus refs X`")   // want `message-commands: Name one next command \(this message names 2\)`
	_ = diag.Errorf(1, tagged)                                           // want `message-tag: Drop the leading 'server:' tag`
	_ = diag.Domain{}.Errorf(1, "[AGENT] read %s", name)                 // want `message-tag: Drop the '\[AGENT\]' marker`
	_ = diag.Format(1, "cache: "+name)                                   // want `message-tag: Drop the leading 'cache:' tag`
	_ = fmt.Errorf("cache: %s; so on, so forth", name)
}

func fields(name string) guard.Verdict {
	_ = guard.Other{Deny: "other: not judged"}
	v := guard.Verdict{
		Deny: fmt.Sprintf("refused because %s ended, so nothing runs", name), // want `message-rationale`
		Lead: errors.New("lead: " + name).Error(),
		Why:  "the rationale field is exempt: it may say why because it is long, so it can stack reasons; which is fine",
	}
	v.Lead = "lead: refused" // want `message-tag: Drop the leading 'lead:' tag`
	v.Why = "why: exempt because x, so y"
	return v
}

func prefixed(name string) string {
	_ = "magus workspace: " + name + " refused because x, so y"        // want `message-rationale`
	_ = fmt.Sprintf("magus workspace: run `a b` or `c d` on %s", name) // want `message-commands`
	return "magus workspace: refused"
}
