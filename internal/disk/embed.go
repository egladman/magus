package disk

import _ "embed"

// ReclaimDiskSource is the magus/reclaimdisk module's Buzz source.
//
//go:embed reclaimdisk.buzz
var ReclaimDiskSource string

// DiskWatchSource is the magus/diskwatch module's Buzz source.
//
//go:embed diskwatch.buzz
var DiskWatchSource string
