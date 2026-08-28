package notificationmodel

import "time"

// NotificationTimestampLayout is fixed-width so timestamps retain temporal
// ordering when persisted in portable TEXT/VARCHAR columns across SQLite,
// PostgreSQL and MySQL adapters.
const NotificationTimestampLayout = "2006-01-02T15:04:05.000000000Z"

func NotificationTimestamp(value time.Time) string {
	return value.UTC().Format(NotificationTimestampLayout)
}
