package notifications

// Runtime owns BFF authorization for the Notification HTTP facade. These are
// host route permissions, not Notification domain contracts.
const (
	PermissionTemplateRead    = "notification.template.read"
	PermissionTemplateManage  = "notification.template.manage"
	PermissionTemplatePublish = "notification.template.publish"
	PermissionTemplateApprove = "notification.template.approve"
	PermissionTemplateTest    = "notification.template.test"
	PermissionPolicyRead      = "notification.policy.read"
	PermissionPolicyManage    = "notification.policy.manage"
)
