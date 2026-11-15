package notes

import "errors"

// ScopeFromFlags turns the --shared/--private pair into the one scope they name, or "" for both.
func ScopeFromFlags(shared, private bool) (string, error) {
	switch {
	case shared && private:
		return "", errors.New("magus notes: --shared and --private are opposites, pass neither to see both")
	case shared:
		return string(ScopeShared), nil
	case private:
		return string(ScopePrivate), nil
	default:
		return "", nil
	}
}
