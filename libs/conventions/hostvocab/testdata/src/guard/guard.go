package guard

// The wire contract never says "Read" or "Bash"; a comment quoting them is fine.

const hookToolRead = "read"

var surfaces = map[string]int{
	"Read":       1, // want `guard code spells the host tool name "Read"`
	hookToolRead: 2,
}

func verdict(tool string) bool {
	switch tool {
	case "Bash": // want `guard code spells the host tool name "Bash"`
		return true
	}
	return false
}
