package service

import "time"

// NotificationClock is the host clock boundary used by the remaining Plane
// inbox compatibility implementation.
type NotificationClock interface{ Now() time.Time }

type notificationSystemClock struct{}

func (notificationSystemClock) Now() time.Time { return time.Now().UTC() }
