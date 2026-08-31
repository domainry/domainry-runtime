package composition

import (
	"context"

	workerplatform "github.com/domainry/domainry-foundation/worker"
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
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
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
	Scheduler             *schedulerapplication.SchedulerApplicationService
	RecordTimers          *recordtimerapplication.RecordTimerApplicationService
}

// Applications exposes the assembled application services through one typed
// composition access point.
func (s *runtimeAssembly) Applications() RuntimeApplications {
	if s == nil {
		scheduler := newSchedulerApplicationService(nil, workerplatform.Dependencies{})
		return RuntimeApplications{Scheduler: scheduler, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(nil, nil, nil)}
	}
	scheduler := s.schedulerService
	if scheduler == nil {
		scheduler = newSchedulerApplicationService(nil, workerplatform.Dependencies{})
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
		Scheduler:             scheduler,
		RecordTimers:          recordTimers,
	}
}

func (s *RuntimeServices) Applications() RuntimeApplications {
	if s == nil {
		scheduler := newSchedulerApplicationService(nil, workerplatform.Dependencies{})
		return RuntimeApplications{Scheduler: scheduler, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(nil, nil, nil)}
	}
	return s.applications
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
