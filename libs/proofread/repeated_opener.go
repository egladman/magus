package proofread

import (
	"fmt"
	"strings"
)

// openerMinItems and openerShare are where the labeled cases in
// repeated-opener.txtar separate.
const (
	openerMinItems = 5
	openerShare    = 0.7
)

// openerSkipped are the words a noun-phrase list opens with by grammar, not
// by habit.
var openerSkipped = map[string]bool{"a": true, "an": true, "the": true}

// repeatedOpener reports each bulleted list of [openerMinItems] or more items
// where one first word opens [openerShare] of them, at the first such item.
// A numbered list is the author's sequence of steps.
func repeatedOpener(in input) []Finding {
	var out []Finding

	for _, l := range lists(in.prose, keep) {
		items := listItems(l)
		if l.numbered() || len(items) < openerMinItems {
			continue
		}

		written := make([]string, len(items))
		keys := make([]string, len(items))

		for i, item := range items {
			written[i] = firstWord(itemBody(boldLead.ReplaceAllString(taskBox.ReplaceAllString(item.text, ""), "")))
			keys[i] = unclassed

			if written[i] != "" {
				keys[i] = strings.ToLower(written[i])
			}
		}

		common := mostCommon(keys)
		if openerSkipped[common.class] || float64(common.count) < openerShare*float64(len(items)) {
			continue
		}

		at := 0
		for keys[at] != common.class {
			at++
		}

		out = append(out, Finding{
			Message: fmt.Sprintf("Open each item on what it says: '%s' starts %d of the list's %d items, a run "+
				"a reader skims past.", written[at], common.count, len(items)),
			Match: written[at], Line: items[at].head.line,
		})
	}

	return out
}
