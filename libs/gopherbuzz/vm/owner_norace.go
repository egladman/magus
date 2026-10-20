//go:build !race

package vm

// poisonReleased is off outside the race detector: a released slot is recycled.
// owner_race.go turns it on.
const poisonReleased = false
