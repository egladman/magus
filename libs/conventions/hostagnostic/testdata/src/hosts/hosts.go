package hosts

import "path/filepath"

func setup(host, root string) []string {
	if host == "cursor" { // want `names an agent host outside a filesystem path`
		return nil
	}
	// Paste this into your Claude settings. // want `names an agent host outside a filesystem path`
	return []string{
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".cursor", "hooks.json"),
	}
}

// Cursor reports where the cursor is, in terminal coordinates.
func Cursor(cursor int) int { return cursor }
