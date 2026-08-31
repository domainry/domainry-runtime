package agenthost

import "time"

type agentTaskClock struct{ now time.Time }

func (c agentTaskClock) Now() time.Time { return c.now }
