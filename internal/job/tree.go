package job

import "github.com/egladman/magus/types"

// TreeLine is one row of a job tree, and how deep its parent chain runs.
type TreeLine struct {
	Job   types.Job
	Depth int
}

// TreeOrder flattens rows parent-first, each followed by the jobs it forked,
// keeping store order among siblings.
//
// Every row comes back. One whose parent was cleared, and one caught in a
// parent cycle, land at depth 0: a job nobody can see is worse than one shown
// without its indentation.
func TreeOrder(rows []types.Job) []TreeLine {
	children := map[string][]types.Job{}
	for _, row := range rows {
		children[row.Parent] = append(children[row.Parent], row)
	}
	out := make([]TreeLine, 0, len(rows))
	emitted := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, row := range children[parent] {
			if emitted[row.ID] {
				continue
			}
			emitted[row.ID] = true
			out = append(out, TreeLine{Job: row, Depth: depth})
			walk(row.ID, depth+1)
		}
	}
	walk("", 0)
	for _, row := range rows {
		if !emitted[row.ID] {
			emitted[row.ID] = true
			out = append(out, TreeLine{Job: row})
		}
	}
	return out
}
