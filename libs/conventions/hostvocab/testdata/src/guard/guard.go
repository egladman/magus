package guard

// The wire contract never says "Read" or "Bash"; a comment quoting them is fine.

const hookToolRead = "read"

var inputs = map[string]int{
	"Read":       1, // want `spells the host tool name "Read": .*; the labels are hookTool\*`
	hookToolRead: 2,
}

func verdict(tool string) bool {
	switch tool {
	case "Bash": // want `spells the host tool name "Bash"`
		return true
	}
	return false
}
