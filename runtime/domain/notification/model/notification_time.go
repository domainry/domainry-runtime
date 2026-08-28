package notificationmodel

import (
	"time"

	notificationcontract "github.com/domainry/domainry-notification-sdk/contract"
)

// NotificationTimestampLayout is fixed-width so timestamps retain temporal
// ordering when persisted in portable TEXT/VARCHAR columns across SQLite,
// PostgreSQL and MySQL adapters.
const NotificationTimestampLayout = notificationcontract.NotificationTimestampLayout

func NotificationTimestamp(value time.Time) string {
	return notificationcontract.NotificationTimestamp(value)
}
