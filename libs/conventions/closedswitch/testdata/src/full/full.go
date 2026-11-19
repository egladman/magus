package full // want `exit names a switch over Color in Gone that this file does not hold`

type Color int

const (
	Red Color = iota
	Green
	Blue
)

func Name(c Color) string {
	switch c {
	case Red:
		return "red"
	case Green, Blue:
		return "other"
	}
	return ""
}

type Lone string

const OnlyOne Lone = "one"

func Single(l Lone) bool {
	switch l {
	case OnlyOne:
		return true
	default:
		return false
	}
}

func Open(n int) int {
	switch n {
	case 1:
		return 1
	default:
		return 0
	}
}
