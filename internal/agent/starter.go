package agent

import "fmt"

// StarterDoc returns an AGENTS.md starter for a developer to paste and own.
//
// The magus guidance arrives inside its begin/end markers (the same bytes
// install prints), so `magus doctor` can grade it once pasted.
func (c *Catalog) StarterDoc(routingIndex bool) string {
	return fmt.Sprintf(`# AGENTS.md

<!-- A starter for AI agents working in this repo. Own and edit this file:
     fill in the project-specific sections below. Everything outside the
     magus:skills markers is yours; magus never writes this file. -->

## Project

<!-- What this repo is, its primary language(s), and where the entry points
     and top-level layout live. A few sentences. -->

## Conventions

<!-- The non-obvious house rules an agent cannot infer from the code:
     naming, error handling, comment style, and what NOT to touch. -->

## Version control

- %s

%s`, VCSSafetyRule, c.AgentsBlock(routingIndex))
}
