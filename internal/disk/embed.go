package disk

import _ "embed"

// ReclaimSource is the magus/reclaimdisk module's Buzz source.
//
//go:embed reclaimdisk.buzz
var ReclaimSource string

// WatchSource is the magus/diskwatch module's Buzz source.
//
//go:embed diskwatch.buzz
var WatchSource string
