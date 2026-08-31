package composition

import (
	"context"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	deployment "github.com/domainry/domainry-runtime/runtime/application/deployment"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	reportquery "github.com/domainry/domainry-runtime/runtime/application/report/query"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/application/report/snapshot"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	surfacecontextbusiness "github.com/domainry/domainry-runtime/runtime/application/surfacecontext"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type RuntimeApplications struct {
	AgentTasks            *agentapplication.AgentTaskRunApplicationService
	AgentInteractiveRuns  *agentapplication.AgentInteractiveRunApplicationService
	NewAgentInteractive   func(*agentapplication.AgentToolGateway) *agentapplication.AgentInteractiveExecutionApplicationService
	AgentTaskWorker       *agentapplication.AgentTaskWorker
	Records               *recordapplication.RecordApplicationService
	Workflows             *workflowapplication.WorkflowApplicationService
	ApplicationSchema     *appschemaapplication.ApplicationSchemaApplicationService
	Automations           *automationapplication.AutomationApplicationService
	Audit                 *auditapplication.AuditApplicationService
	Actions               *actionapplication.ActionApplicationService
	RuntimeStatus         *deployment.DeploymentRuntimeStatusApplicationService
	SurfaceContext        *surfacecontextbusiness.SurfaceContextApplicationService
	BusinessSystem        *businesssystemapplication.BusinessSystemApplicationService
	PublicationHandoff    *publicationhandoff.PublicationHandoffApplicationService
	Lifecycle             *lifecycleapplication.LifecycleApplicationService
	Schema                *appschemaapplication.ApplicationSchemaQueryApplicationService
	AuthoringCapabilities *capabilityapplication.CapabilityAuthoringApplicationService
	BusinessReferences    *changeplanapplication.ChangePlanReferenceApplicationService
	ReportSnapshots       *reportsnapshot.ReportSnapshotApplicationService
	ReportQueries         *reportquery.ReportQueryApplicationService
	ReportExports         *reportexportapplication.ReportExportApplicationService
	Scheduler             *schedulerapplication.SchedulerApplicationService
	RecordTimers          *recordtimerapplication.RecordTimerApplicationService
}

// Applications exposes the assembled application services through one typed
// composition access point.
func (s *runtimeAssembly) Applications() RuntimeApplications {
	if s == nil {
		scheduler := newSchedulerApplicationService(nil, nil, nil, nil, workerplatform.Dependencies{})
		return RuntimeApplications{Scheduler: scheduler, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(scheduler)}
	}
	scheduler := s.schedulerService
	if scheduler == nil {
		scheduler = newSchedulerApplicationService(nil, nil, nil, nil, workerplatform.Dependencies{})
	}
	recordTimers := s.recordTimerService
	if recordTimers == nil {
		recordTimers = recordtimerapplication.NewRecordTimerApplicationService(scheduler)
	}
	return RuntimeApplications{
		AgentTasks:            s.agentTaskRunService,
		AgentInteractiveRuns:  s.agentInteractiveRunService,
		NewAgentInteractive:   s.newAgentInteractiveExecution,
		AgentTaskWorker:       s.agentTaskWorker,
		Records:               s.recordApplicationService,
		Workflows:             s.workflowApplicationService,
		ApplicationSchema:     s.applicationSchemaService,
		Automations:           s.automationApplicationService,
		Audit:                 s.auditApplicationService,
		Actions:               s.actionService,
		RuntimeStatus:         s.runtimeStatusService,
		SurfaceContext:        s.surfaceContextService,
		BusinessSystem:        s.businessSystemService,
		PublicationHandoff:    s.publicationHandoffService,
		Schema:                s.schemaService,
		AuthoringCapabilities: s.authoringCapabilities,
		BusinessReferences:    s.businessReferences,
		ReportSnapshots:       s.reportSnapshotsService,
		ReportQueries:         s.reportQueriesService,
		ReportExports:         s.reportExportsService,
		Scheduler:             scheduler,
		RecordTimers:          recordTimers,
	}
}

func (s *RuntimeServices) Applications() RuntimeApplications {
	if s == nil {
		scheduler := newSchedulerApplicationService(nil, nil, nil, nil, workerplatform.Dependencies{})
		return RuntimeApplications{Scheduler: scheduler, RecordTimers: recordtimerapplication.NewRecordTimerApplicationService(scheduler)}
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
