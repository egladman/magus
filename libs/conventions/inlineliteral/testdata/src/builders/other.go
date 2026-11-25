package builders

func elsewhere() {
	b := builder() // want `builder returns one composite literal and has one use site: inline it`
	_ = b
}
