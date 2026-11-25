package ledgered // want `exit for gone in ledgered/ledgered.go names no helper`

type point struct{ x int }

func kept() point { return point{} }

func use() point { return kept() }
