package contract

// Notification administration permissions are host authorization contracts;
// they are intentionally not part of the extracted notification domain.
const (
	PermissionTemplateRead    = "notification.template.read"
	PermissionTemplateManage  = "notification.template.manage"
	PermissionTemplatePublish = "notification.template.publish"
	PermissionTemplateApprove = "notification.template.approve"
	PermissionTemplateTest    = "notification.template.test"
	PermissionPolicyRead      = "notification.policy.read"
	PermissionPolicyManage    = "notification.policy.manage"
)
