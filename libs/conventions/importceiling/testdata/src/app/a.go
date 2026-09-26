package main // want `app imports 3 packages under engine/, over its ceiling of 2`

import (
	_ "engine/cache"
	_ "engine/graph"
)

func main() {}
