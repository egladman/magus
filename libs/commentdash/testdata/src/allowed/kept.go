// Package allowed checks that the glob excuses a file rather than a comment.
package allowed

// Kept sits in a file no glob names, so its aside is reported - like this. // want `em-dash aside`
func Kept() {}
