package composition

import (
	"context"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	deployment "github.com/domainry/domainry-runtime/runtime/application/deployment"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RuntimeApplications struct {
	AgentAuthorization    *agentapplication.AgentAuthorizationApplicationService
	AgentTaskDispatch     *agentapplication.AgentTaskDispatchApplicationService
	Records               *recordapplication.RecordApplicationService
	Workflows             *workflowapplication.WorkflowApplicationService
	ApplicationSchema     *appschemaapplication.ApplicationSchemaApplicationService
	Automations           *automationapplication.AutomationApplicationService
	Audit                 *auditapplication.AuditApplicationService
	Actions               *actionapplication.ActionApplicationService
	RuntimeStatus         *deployment.DeploymentRuntimeStatusApplicationService
	BusinessSystem        *businesssystemapplication.BusinessSystemApplicationService
	PublicationHandoff    *publicationhandoff.PublicationHandoffApplicationService
	Schema                *appschemaapplication.ApplicationSchemaQueryApplicationService
	AuthoringCapabilities *capabilityapplication.CapabilityAuthoringApplicationService
	BusinessReferences    *changeplanapplication.ChangePlanReferenceApplicationService
	Reports               reportsdk.ApplicationBinding
	TargetExecutions      *dispatchapplication.TargetExecutionApplicationService
	RecordTimers          *recordtimerapplication.RecordTimerApplicationService
}

// Applications exposes the assembled application services through one typed
// composition access point.
func (s *runtimeAssembly) Applications() RuntimeApplications {
	if s == nil {
		targetExecutions := dispatchapplication.NewTargetExecutionApplicationService(nil)
		return RuntimeApplications{TargetExecutions: targetExecutions, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(nil, nil, nil)}
	}
	targetExecutions := s.targetExecutionService
	if targetExecutions == nil {
		targetExecutions = dispatchapplication.NewTargetExecutionApplicationService(nil)
	}
	recordTimers := s.recordTimerService
	if recordTimers == nil {
		recordTimers = recordtimerapplication.NewRecordTimerApplicationService(nil, nil, nil)
	}
	return RuntimeApplications{
		AgentAuthorization:    s.agentAuthorizationService,
		AgentTaskDispatch:     s.agentTaskDispatchService,
		Records:               s.recordApplicationService,
		Workflows:             s.workflowApplicationService,
		ApplicationSchema:     s.applicationSchemaService,
		Automations:           s.automationApplicationService,
		Audit:                 s.auditApplicationService,
		Actions:               s.actionService,
		RuntimeStatus:         s.runtimeStatusService,
		BusinessSystem:        s.businessSystemService,
		PublicationHandoff:    s.publicationHandoffService,
		Schema:                s.schemaService,
		AuthoringCapabilities: s.authoringCapabilities,
		BusinessReferences:    s.businessReferences,
		Reports:               s.reportApplication,
		TargetExecutions:      targetExecutions,
		RecordTimers:          recordTimers,
	}
}

func (s *RuntimeServices) Applications() RuntimeApplications {
	if s == nil {
		targetExecutions := dispatchapplication.NewTargetExecutionApplicationService(nil)
		return RuntimeApplications{TargetExecutions: targetExecutions, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(nil, nil, nil)}
	}
	return s.applications
}

// NotificationEventPublisher exposes the assembled Notification SDK producer
// port to startup composition. Source modules still receive only their own
// neutral event contracts through Runtime adapters.
func (s *RuntimeServices) NotificationEventPublisher() func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
	if s == nil {
		return nil
	}
	return s.notificationEventPublisher
}

// SchedulerDefinitionSource exposes only the host-supplied definition
// projection used to reconcile the external Scheduler owner. It is not a
// Runtime application service or scheduling entrypoint.
func (s *RuntimeServices) SchedulerDefinitionSource() SchedulerDefinitionSource {
	if s == nil {
		return schedulerDefinitionSourceAdapter{}
	}
	return s.schedulerDefinitionSource
}

func (s *RuntimeServices) Schema() appschemamodel.ApplicationSchemaSnapshot {
	if s == nil || s.schema == nil {
		return appschemamodel.ApplicationSchemaSnapshot{}
	}
	return s.schema.Schema()
}

func (s *RuntimeServices) SchemaForPrincipal(ctx context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	if s == nil || s.schema == nil {
		return appschemamodel.ApplicationSchemaSnapshot{}
	}
	return s.schema.SchemaForPrincipal(ctx, principal)
}
