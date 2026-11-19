package gaps

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func Missing(c Color) string {
	switch c { // want `switch over Color misses Blue: add .case Blue:.; Missing holds 1 such switches and the exhaustive-switch baseline records 0`
	case Red, Green:
		return "x"
	}
	return ""
}

func Defaulted(c Color) string {
	switch c { // want `switch over Color names every member and keeps a default arm: delete the default arm`
	case Red, Green, Blue:
		return "x"
	default:
		return ""
	}
}

func Both(c Color) string {
	switch c { // want `switch over Color misses Blue, Green and keeps a default arm: add .case Blue, Green:. and delete the default arm`
	case Red:
		return "x"
	default:
		return ""
	}
}
