package held

type Op int

const (
	Add Op = iota
	Sub
)

func Eval(o Op) int {
	switch o {
	case Add:
		return 1
	default:
		return 0
	}
}

func Count(o Op) int {
	switch o {
	case Add:
		return 1
	}
	return 0
}

func Fixed(o Op) int {
	switch o {
	case Add, Sub:
		return 1
	}
	return 0
}
