package model

// NotificationAudienceResolverWorkflowTaskAssignee is the Runtime-hosted
// resolver that derives the current Workflow task assignee from a Notification
// subject. Notification owns event/rule semantics; Runtime only publishes the
// host implementations it actually mounts.
const NotificationAudienceResolverWorkflowTaskAssignee = "workflow_task_assignee"

var runtimeNotificationAudienceResolverKeys = []string{NotificationAudienceResolverWorkflowTaskAssignee}

// NotificationAudienceResolverKeys returns a detached copy of the complete
// Runtime-hosted resolver inventory.
func NotificationAudienceResolverKeys() []string {
	return append([]string(nil), runtimeNotificationAudienceResolverKeys...)
}
