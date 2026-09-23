package database

import "strings"

const (
	WorkerScopeOwnerAgentConversationCapacity = "agent_conversation_capacity"
	WorkerScopeOwnerAgentTask                 = "agent_task"
	WorkerScopeOwnerDataExchange              = "data_exchange"
	WorkerScopeOwnerIdempotencyCleanup        = "idempotency_cleanup"
	WorkerScopeOwnerNotificationChannel       = "notification_channel"
	WorkerScopeOwnerNotificationInbox         = "notification_inbox"
	WorkerScopeOwnerRuntimePublicationOutbox  = "runtime_publication_outbox"
	WorkerScopeOwnerSchedulerTriggerCapacity  = "scheduler_trigger_capacity"
	WorkerScopeOwnerWorkflowContinuation      = "workflow_continuation"
)

type WorkerScopeRegistration struct {
	Owner          string
	RecoveryPolicy string
}

var workerScopeRegistry = map[string]WorkerScopeRegistration{
	WorkerScopeOwnerAgentConversationCapacity: {Owner: WorkerScopeOwnerAgentConversationCapacity, RecoveryPolicy: "transactional_guard"},
	WorkerScopeOwnerAgentTask:                 {Owner: WorkerScopeOwnerAgentTask, RecoveryPolicy: "durable_due_scan"},
	WorkerScopeOwnerDataExchange:              {Owner: WorkerScopeOwnerDataExchange, RecoveryPolicy: "durable_due_scan"},
	WorkerScopeOwnerIdempotencyCleanup:        {Owner: WorkerScopeOwnerIdempotencyCleanup, RecoveryPolicy: "expired_lease_reclaim"},
	WorkerScopeOwnerNotificationChannel:       {Owner: WorkerScopeOwnerNotificationChannel, RecoveryPolicy: "durable_due_scan"},
	WorkerScopeOwnerNotificationInbox:         {Owner: WorkerScopeOwnerNotificationInbox, RecoveryPolicy: "durable_due_scan"},
	WorkerScopeOwnerRuntimePublicationOutbox:  {Owner: WorkerScopeOwnerRuntimePublicationOutbox, RecoveryPolicy: "durable_due_scan"},
	WorkerScopeOwnerSchedulerTriggerCapacity:  {Owner: WorkerScopeOwnerSchedulerTriggerCapacity, RecoveryPolicy: "transactional_guard"},
	WorkerScopeOwnerWorkflowContinuation:      {Owner: WorkerScopeOwnerWorkflowContinuation, RecoveryPolicy: "durable_due_scan"},
}

func WorkerScopeRegistrationFor(owner string) (WorkerScopeRegistration, bool) {
	registration, found := workerScopeRegistry[strings.TrimSpace(owner)]
	return registration, found
}
