package postag

// XPOS is a Penn Treebank part-of-speech tag, as EWT spells it. The zero value
// is no tag.
type XPOS string

// The tags callers compare against. The model predicts the rest too, so a
// tag missing here is still a valid XPOS.
const (
	VB    XPOS = "VB"
	VBD   XPOS = "VBD"
	VBG   XPOS = "VBG"
	VBN   XPOS = "VBN"
	VBP   XPOS = "VBP"
	VBZ   XPOS = "VBZ"
	MD    XPOS = "MD"
	COMMA XPOS = ","
)
