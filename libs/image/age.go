package image

import "time"

// setCreated records config.Created on the Layout as RFC 3339 UTC.
// An absent Created leaves Layout.Created empty; callers speak that, not us.
func setCreated(l *Layout, created *time.Time) {
	if created == nil {
		return
	}
	l.Created = created.UTC().Format(time.RFC3339)
}
