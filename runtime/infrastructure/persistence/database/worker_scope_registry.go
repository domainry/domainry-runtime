package database

import sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"

const (
	WorkerScopeOwnerAgentConversationCapacity = sharedworkerscope.OwnerAgentConversationCapacity
	WorkerScopeOwnerAgentTask                 = sharedworkerscope.OwnerAgentTask
	WorkerScopeOwnerDataExchange              = sharedworkerscope.OwnerDataExchange
	WorkerScopeOwnerIdempotencyCleanup        = sharedworkerscope.OwnerIdempotencyCleanup
	WorkerScopeOwnerNotificationChannel       = sharedworkerscope.OwnerNotificationChannel
	WorkerScopeOwnerNotificationInbox         = sharedworkerscope.OwnerNotificationInbox
	WorkerScopeOwnerRuntimePublicationOutbox  = sharedworkerscope.OwnerRuntimePublicationOutbox
	WorkerScopeOwnerSchedulerTriggerCapacity  = sharedworkerscope.OwnerSchedulerTriggerCapacity
	WorkerScopeOwnerWorkflowContinuation      = sharedworkerscope.OwnerWorkflowContinuation
)

type WorkerScopeRegistration = sharedworkerscope.Registration

func WorkerScopeRegistrationFor(owner string) (WorkerScopeRegistration, bool) {
	return sharedworkerscope.RegistrationFor(owner)
}
