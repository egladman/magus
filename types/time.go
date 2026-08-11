package types

// TimeLayout names a timestamp format a time\format or time\parse call uses.
//
// Go's reference-layout scheme - spelling a format by writing out the reference
// instant, "2006-01-02T15:04:05Z07:00" - is unguessable for anyone who has not
// written Go, and mistyping one digit yields a format that parses and renders the
// wrong thing. These cases name the layouts Go's own time package defines, so a
// magusfile writes TimeLayout.rfc3339 instead.
//
// IT DOES NOT CLOSE THE PARAMETER. A Buzz enum<str> accepts a plain string where
// it is declared, so a custom layout still works: the enum adds names for the
// common formats without taking away the escape hatch, which matters because
// there is no finite set of timestamp formats a build might have to read.
type TimeLayout string

const (
	// TimeRFC3339 is the interchange default: 2006-01-02T15:04:05Z07:00. Use it
	// for anything another program will read.
	TimeRFC3339 TimeLayout = "2006-01-02T15:04:05Z07:00"
	// TimeRFC3339Nano is RFC 3339 with nanoseconds, for ordering events that can
	// occur within the same second.
	TimeRFC3339Nano TimeLayout = "2006-01-02T15:04:05.999999999Z07:00"
	// TimeDateOnly is 2006-01-02, for a changelog heading or a directory name.
	TimeDateOnly TimeLayout = "2006-01-02"
	// TimeTimeOnly is 15:04:05.
	TimeTimeOnly TimeLayout = "15:04:05"
	// TimeDateTime is 2006-01-02 15:04:05, the human-readable pairing.
	TimeDateTime TimeLayout = "2006-01-02 15:04:05"
	// TimeRFC1123 is the HTTP date format, for a Last-Modified or Expires header.
	TimeRFC1123 TimeLayout = "Mon, 02 Jan 2006 15:04:05 MST"
	// TimeKitchen is 3:04PM, for output a person reads at a glance.
	TimeKitchen TimeLayout = "3:04PM"
)
