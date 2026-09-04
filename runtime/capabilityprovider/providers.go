// Package capabilityprovider adapts Runtime-owned domains to the same
// deployment-neutral capability Binding implemented by external module SDKs.
// These adapters are immutable contract constructors; they do not open a
// Runtime, mount transport, or introduce another provider lifecycle.
package capabilityprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulecapability"
	actionprojection "github.com/domainry/domainry-runtime/runtime/domain/action/projection"
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
	profilebindingcontract "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/contract"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	runtimeopenapi "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
)

const runtimeProviderRevision = "runtime-native-capability-v1"

type endpointSelector func(endpointmodel.RuntimeEndpointContractV1) bool

type categorySpec struct {
	key, name, description string
	chains                 []string
	scopes                 []string
	validationContracts    []modulecapability.ValidationScopeContract
	selectEndpoints        endpointSelector
	actions                []actioncontract.ActionDefinition
	projections            []modulecapability.SourceProjection
}

type providerSpec struct {
	key, sourceOwner, name, description string
	scenarios                           modulecapability.AdaptationScenarios
	categories                          []categorySpec
	validator                           modulecapability.Validator
}

// Bindings constructs the complete deterministic Runtime-owned provider set.
// External SDK modules are deliberately not included here.
func Bindings() ([]modulecapability.Binding, error) {
	specs, err := providerSpecs()
	if err != nil {
		return nil, err
	}
	document := runtimeopenapi.Build(appschemamodel.ApplicationSchemaSnapshot{})
	result := make([]modulecapability.Binding, 0, len(specs))
	for _, spec := range specs {
		binding, buildErr := buildProvider(document, spec)
		if buildErr != nil {
			return nil, fmt.Errorf("build Runtime capability provider %q: %w", spec.key, buildErr)
		}
		result = append(result, binding)
	}
	return result, nil
}

func providerSpecs() ([]providerSpec, error) {
	projection := func(domain capabilitycontract.CapabilityAuthoringDomain, keys ...string) ([]modulecapability.SourceProjection, error) {
		if len(keys) == 0 {
			return authoringProjections(domain.Capabilities)
		}
		selected := make([]capabilitycontract.CapabilityAuthoringDefinition, 0, len(keys))
		for _, definition := range domain.Capabilities {
			for _, key := range keys {
				if definition.Key == key {
					selected = append(selected, definition)
				}
			}
		}
		return authoringProjections(selected)
	}
	schema, err := projection(appschemacontract.ApplicationSchemaAuthoringDomain(), "schema.object")
	if err != nil {
		return nil, err
	}
	actions, err := projection(actionprojection.ActionAuthoringDomain(), "action.definition")
	if err != nil {
		return nil, err
	}
	workflows, err := projection(workflowpolicy.WorkflowAuthoringDomain(), "workflow.definition")
	if err != nil {
		return nil, err
	}
	automations, err := projection(automationpolicy.AutomationAuthoringDomain(), "automation.rule")
	if err != nil {
		return nil, err
	}
	profileBindings, err := authoringProjections([]capabilitycontract.CapabilityAuthoringDefinition{profilebindingcontract.ProfileBindingAuthoringCapability()})
	if err != nil {
		return nil, err
	}
	recordExportAPI, err := modelAPIProjection("record_export")
	if err != nil {
		return nil, err
	}
	owner := func(values ...string) endpointSelector {
		accepted := map[string]bool{}
		for _, value := range values {
			accepted[value] = true
		}
		return func(contract endpointmodel.RuntimeEndpointContractV1) bool { return accepted[endpointOwner(contract)] }
	}
	ownerAndPath := func(value string, accept func(string) bool) endpointSelector {
		return func(contract endpointmodel.RuntimeEndpointContractV1) bool {
			_, path, _ := strings.Cut(contract.EndpointIdentity, " ")
			return endpointOwner(contract) == value && accept(path)
		}
	}
	return []providerSpec{
		{
			key: "action_registry", sourceOwner: "action", name: "Runtime Action registry", description: "Signed host query for live uses of source-owned Action permissions before Identity governance changes.",
			scenarios: scenarios(
				[]string{"Identity must inspect live Runtime uses before changing or retiring an Action permission"},
				[]string{"The caller wants to author a business Action or invoke one against a record"},
				[]string{"Action permission usage", "permission retirement impact"},
				[]string{"action.permission_usage"}, []string{"identity"}, []string{"records"},
				[]string{"identity_permission_change_to_runtime_usage_query"}, nil,
				"Check whether a permission is still used before retiring it", "The Runtime Action registry owns the live installed-use projection",
				"Approve an order", "Records and the source-owned Action handler own business execution"),
			categories: []categorySpec{{key: "action_registry.usage", name: "Action permission usage", description: "Query live Runtime Action permission uses through the signed Identity-to-Runtime protocol.", chains: []string{"identity_permission_change_to_runtime_usage_query"}, actions: []actioncontract.ActionDefinition{hostsurfacemodel.PermissionUsageQueryAction("")}}},
		},
		{
			key: "runtime_schema", sourceOwner: "appschema", name: "Runtime schema", description: "Business-neutral object definitions with embedded fields, relations, validations, and exact-number metadata owned by Runtime Application Schema.",
			scenarios: scenarios(
				[]string{"A PRD needs business objects, typed fields, relations, dictionaries, validation constraints, or exact decimal metadata"},
				[]string{"The requirement only consumes records from an already-defined object or only localizes an owner definition"},
				[]string{"business object", "field type", "relation", "currency precision"},
				[]string{"schema.object", "schema.field", "schema.relation"}, []string{"identity"}, []string{"metadata"},
				[]string{"prd_entity_to_runtime_schema", "schema_before_records_actions_workflow_and_report"}, []string{"schema.object"},
				"Define orders and line items with an exact currency amount and a customer relation", "Runtime schema owns the complete object fragment, including fields, relations, and validation metadata",
				"Translate an existing field label", "Metadata projects localization; it does not own the field definition"),
			categories: []categorySpec{
				{key: "schema.authoring", name: "Schema authoring", description: "Author and validate complete project object fragments and inspect the embedded field, relation, and dictionary schemas owned by Runtime Application Schema.", chains: []string{"prd_entity_to_runtime_schema"}, scopes: []string{"schema.object"}, validationContracts: []modulecapability.ValidationScopeContract{{
					Kind: "schema.object", Description: "Validate one complete project object definition, including its embedded fields and relations.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"objects"}, ReferencedCollections: []string{"objects"},
				}}, selectEndpoints: ownerAndPath("appschema", func(path string) bool {
					return strings.Contains(path, "/definitions/") && strings.HasSuffix(path, "/validate")
				}), projections: schema},
				{key: "schema.instances", name: "Application schema instance evidence", description: "Inspect installed-schema migration planning, object record counts, and Runtime-local schema diagnostics.", chains: []string{"schema_before_records_actions_workflow_and_report"}, selectEndpoints: ownerAndPath("appschema", func(path string) bool {
					return !strings.Contains(path, "/definitions/") || !strings.HasSuffix(path, "/validate")
				})},
			}, validator: validateSchemaCandidate,
		},
		{
			key: "records", sourceOwner: "records", name: "Records and Actions", description: "Transactional business records and source-owned Action invocation over Runtime schema.",
			scenarios: scenarios(
				[]string{"A PRD needs CRUD, filtered record lists, related records, bulk record jobs, or deterministic business Actions"},
				[]string{"The requirement is analytical aggregation, a human approval graph, or external-provider integration rather than transactional record behavior"},
				[]string{"create update delete record", "record list", "record export", "business action", "bulk records", "related records"},
				[]string{"records.crud", "records.query", "records.batch", "action.definition", "action.execute"}, []string{"identity", "runtime_schema"}, []string{"audit", "data_exchange", "workflow"},
				[]string{"schema_to_records", "action_definition_to_guarded_execution", "record_mutation_to_audit_and_realtime"}, []string{"action.definition"},
				"Maintain orders and expose a guarded approve Action", "Records owns transactional state while the Action definition and generated handler own the business effect",
				"Show monthly revenue grouped by region", "Report owns analytical definitions and grouped execution"),
			categories: []categorySpec{
				{key: "records.authoring", name: "Action authoring", description: "Author Runtime Action metadata while project-owned handlers retain business behavior.", chains: []string{"action_definition_to_guarded_execution"}, scopes: []string{"action.definition"}, validationContracts: []modulecapability.ValidationScopeContract{{
					Kind: "action.definition", Description: "Validate one project Action definition against its referenced object contract.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"actions"}, ReferencedCollections: []string{"objects"},
				}}, projections: actions},
				{key: "records.business", name: "Business records and Actions", description: "Query and mutate records and invoke object- or record-scoped Actions under dynamic owner policy.", chains: []string{"schema_to_records", "record_mutation_to_audit_and_realtime", "action_definition_to_guarded_execution"}, selectEndpoints: ownerAndPath("records", func(path string) bool {
					return !strings.Contains(path, "/profile/") && !strings.Contains(path, "/action-assurance/")
				})},
				{key: "records.assurance", name: "Action assurance", description: "Issue identity-backed step-up challenges and exchange verified proof for a short-lived, action-bound grant.", chains: []string{"action_definition_to_guarded_execution"}, selectEndpoints: ownerAndPath("records", func(path string) bool { return strings.Contains(path, "/action-assurance/") })},
				{key: "records.profile", name: "Business profile lifecycle", description: "Deactivate and reactivate business profiles while preserving their Identity boundary.", chains: []string{"record_mutation_to_audit_and_realtime"}, selectEndpoints: ownerAndPath("records", func(path string) bool { return strings.Contains(path, "/profile/") })},
				{key: "records.export", name: "Record export", description: "Request one authorized record export; Runtime owns delivery and completion handling.", chains: []string{"schema_to_records"}, projections: recordExportAPI},
			}, validator: validateActionCandidate,
		},
		{
			key: "workflow", sourceOwner: "workflows", name: "Workflow", description: "Published human and system workflow graphs, tasks, decisions, timers, and execution evidence.",
			scenarios: scenarios(
				[]string{"A PRD needs approvals, assignments, branching, human tasks, workflow timers, or durable process state"},
				[]string{"The requirement is one deterministic source-owned Action or a simple recurring clock trigger"},
				[]string{"approval flow", "human task", "assignee", "workflow graph", "branch", "process status"},
				[]string{"workflow.definition", "workflow.graph_v2", "workflow.task", "workflow.decision", "workflow.timer"}, []string{"identity", "records"}, []string{"agent", "notification", "scheduler"},
				[]string{"record_or_action_to_workflow_process", "workflow_task_to_identity_assignee", "workflow_timer_to_scheduler_clock"}, []string{"workflow.definition"},
				"Route an expense through manager approval and finance review", "Workflow owns the graph, assignments, task decisions, and durable process evidence",
				"Recalculate a field as part of a governed record mutation", "A source-owned Handler owns the mutation and derived business value"),
			categories: []categorySpec{
				{key: "workflow.authoring", name: "Workflow authoring", description: "Author and validate one complete workflow definition and inspect the embedded graph, node, resolver, trigger, condition, and edge schemas.", chains: []string{"record_or_action_to_workflow_process"}, scopes: []string{"workflow.definition"}, validationContracts: []modulecapability.ValidationScopeContract{{
					Kind: "workflow.definition", Description: "Validate one complete project workflow definition.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"workflows"}, ReferencedCollections: []string{"actions", "objects"},
				}}, selectEndpoints: ownerAndPath("workflows", func(path string) bool {
					return strings.Contains(path, "/authoring-fragments/") || strings.HasSuffix(path, "/validate") || strings.HasSuffix(path, "/simulate")
				}), projections: workflows},
				{key: "workflow.participant", name: "Participant workflow", description: "Start and inspect principal-visible workflow processes and tasks.", chains: []string{"workflow_task_to_identity_assignee"}, selectEndpoints: ownerAndPath("workflows", func(path string) bool {
					return !strings.HasPrefix(path, "/workflow/recovery/") && !strings.Contains(path, "/authoring-fragments/") && !strings.HasSuffix(path, "/validate") && !strings.HasSuffix(path, "/simulate")
				})},
				{key: "workflow.management", name: "Workflow recovery", description: "Inspect and operate failed workflow processes and executions.", chains: []string{"record_or_action_to_workflow_process", "workflow_timer_to_scheduler_clock"}, selectEndpoints: ownerAndPath("workflows", func(path string) bool { return strings.HasPrefix(path, "/workflow/recovery/") })},
			}, validator: validateWorkflowCandidate,
		},
		{
			key: "automation", sourceOwner: "automation", name: "Automation", description: "Deterministic record-lifecycle rules, conditions, instructions, simulation, and execution evidence.",
			scenarios: scenarios(
				[]string{"A PRD needs an automatic reaction to record creation, update, deletion, or field transition with deterministic instructions"},
				[]string{"The requirement needs a human approval graph, natural-language reasoning, or a recurring time schedule without a record event"},
				[]string{"when record changes", "automatic rule", "condition and instruction", "derive fields", "emit event"},
				[]string{"automation.rule", "automation.trigger", "automation.condition_group", "automation.instruction"}, []string{"identity", "records", "runtime_schema"}, []string{"integration", "notification", "workflow"},
				[]string{"record_lifecycle_to_automation_rule", "automation_instruction_to_action_workflow_or_event"}, []string{"automation.rule"},
				"When an order becomes ready, derive its routing state and start an approval", "Automation owns deterministic lifecycle triggers, conditions, and instruction dispatch",
				"Run a report every weekday morning", "Scheduler owns clock-based recurrence"),
			categories: []categorySpec{{key: "automation.rules", name: "Automation rules", description: "Author, validate, simulate, inspect, and execute record-lifecycle automation rules.", chains: []string{"record_lifecycle_to_automation_rule", "automation_instruction_to_action_workflow_or_event"}, scopes: []string{"automation.rule"}, validationContracts: []modulecapability.ValidationScopeContract{{
				Kind: "automation.rule", Description: "Validate one complete project Automation rule against referenced objects, Actions, and Workflows.", Coverage: modulecapability.ValidationCoverageAllCandidates, CandidateCollections: []string{"automation_rules"}, ReferencedCollections: []string{"actions", "objects", "workflows"},
			}}, selectEndpoints: owner("automation"), projections: automations}}, validator: validateAutomationCandidate,
		},
		{
			key: "realtime", sourceOwner: "businessevents", name: "Realtime business events", description: "Tenant-scoped SSE refresh signals that instruct clients to refetch authorized durable state.",
			scenarios: scenarios(
				[]string{"A PRD needs live refresh after business changes or resumable tenant-scoped update signals"},
				[]string{"The requirement needs durable event processing, notification delivery, or an immutable audit history rather than UI refresh"},
				[]string{"live refresh", "SSE", "resume cursor", "real-time record updates"},
				[]string{"realtime.business_refresh"}, []string{"identity"}, []string{"records"},
				[]string{"business_mutation_to_realtime_refresh_to_authorized_refetch"}, nil,
				"Refresh an order workbench when another actor changes the order", "Realtime emits bounded refresh signals and the client refetches through authorized APIs",
				"Send an email when an order changes", "Notification and Integration own durable user delivery, not Realtime refresh"),
			categories: []categorySpec{{key: "realtime.events", name: "Realtime refresh stream", description: "Subscribe to resumable business refresh and resync signals.", chains: []string{"business_mutation_to_realtime_refresh_to_authorized_refetch"}, selectEndpoints: owner("businessevents")}},
		},
		{
			key: "uploads", sourceOwner: "uploads", name: "Uploads", description: "Authorized file upload, content access, and scan-status contracts for participant workflows.",
			scenarios: scenarios(
				[]string{"A PRD needs users to attach files to business records or requires virus-scan status before use"},
				[]string{"The requirement is asynchronous bulk import/export or durable report artifact generation"},
				[]string{"file attachment", "upload", "download file", "virus scan"},
				[]string{"uploads.create", "uploads.read", "uploads.scan_status"}, []string{"identity"}, []string{"data_exchange", "records"},
				[]string{"business_attachment_to_upload_to_scan_gate"}, nil,
				"Attach a signed document to a case and block use until scanning completes", "Uploads owns file acceptance, authorized content access, and scan status",
				"Import a million CSV rows with progress and cancellation", "Data Exchange owns durable batch transfer jobs"),
			categories: []categorySpec{{key: "uploads.files", name: "Business files", description: "Upload, read, and inspect scan status for business files.", chains: []string{"business_attachment_to_upload_to_scan_gate"}, selectEndpoints: owner("uploads")}},
		},
		{
			key: "discovery", sourceOwner: "discovery", name: "Runtime discovery", description: "Principal-shaped Runtime schema and localization resources for dynamic clients.",
			scenarios: scenarios(
				[]string{"A product client must discover its visible Runtime schema or localized resource catalog dynamically"},
				[]string{"The model is defining schema, or an operator only needs liveness and diagnostics"},
				[]string{"runtime schema discovery", "dynamic client metadata", "locale resources"},
				[]string{"discovery.runtime_schema", "discovery.localization"}, []string{"identity"}, []string{"metadata"},
				[]string{"identity_visibility_to_runtime_schema_to_dynamic_client"}, nil,
				"Build a workbench from the current principal-visible object and action catalog", "Discovery owns principal-shaped Runtime schema projection",
				"Define a new order object", "Runtime schema authoring owns the definition; Discovery only projects installed state"),
			categories: []categorySpec{{key: "discovery.runtime", name: "Runtime schema discovery", description: "Read principal-shaped Runtime schema, localization resources, and the installed module inventory.", chains: []string{"identity_visibility_to_runtime_schema_to_dynamic_client"}, selectEndpoints: owner("discovery"), actions: []actioncontract.ActionDefinition{hostsurfacemodel.ModuleInventoryAction()}}},
		},
		{
			key: "publication", sourceOwner: "publicationhandoff", name: "Publication handoff", description: "Durable acceptance and lookup of source-owner publication messages across Runtime boundaries.",
			scenarios: scenarios(
				[]string{"A PRD needs a durable source-owner publication handoff whose receipt is inspected by a business client"},
				[]string{"The requirement only emits a transient UI refresh signal or directly invokes a synchronous Action"},
				[]string{"publication handoff", "durable publication receipt", "message acceptance"},
				[]string{"publication.handoff_receipt"}, []string{"identity"}, []string{"integration", "notification"},
				[]string{"source_publication_to_runtime_handoff_receipt"}, nil,
				"Show whether an accepted source publication handoff completed", "Publication owns the durable handoff receipt exposed to business clients",
				"Refresh a screen after a record update", "Realtime refresh is sufficient without a durable publication handoff"),
			categories: []categorySpec{{key: "publication.handoffs", name: "Publication handoffs", description: "Inspect durable business publication handoff receipts.", chains: []string{"source_publication_to_runtime_handoff_receipt"}, selectEndpoints: owner("publicationhandoff")}},
		},
		{
			key: "profile_binding", sourceOwner: "profilebinding", name: "Business profile binding", description: "Bind an Identity principal to a Runtime business-profile object and its lifecycle semantics.",
			scenarios: scenarios(
				[]string{"A PRD extends authenticated users with business profile fields, claim mappings, profile listings, or profile activation state"},
				[]string{"The requirement only stores a business person without login or only configures authentication roles"},
				[]string{"employee profile extension", "identity relation field", "business profile claim", "profile listing"},
				[]string{"principal.profile_binding"}, []string{"identity", "records", "runtime_schema"}, nil,
				[]string{"identity_principal_to_business_profile_binding"}, []string{"principal.profile_binding"},
				"Give each signed-in employee a one-to-one staff profile with business claims", "Profile binding owns the bridge between Identity and the Runtime business object",
				"Store external customer contacts who never sign in", "Runtime Records owns business data without an Identity profile binding"),
			categories: []categorySpec{{key: "profile_binding.authoring", name: "Profile binding authoring", description: "Validate the Identity-to-business-profile binding embedded in a project object's ux.config.", chains: []string{"identity_principal_to_business_profile_binding"}, scopes: []string{"principal.profile_binding"}, validationContracts: []modulecapability.ValidationScopeContract{{
				Kind: "principal.profile_binding", Description: "Validate a project object whose ux.kind is identity_profile_extension.", Coverage: modulecapability.ValidationCoverageExplicit, CandidateCollections: []string{"objects"}, ReferencedCollections: []string{"objects"},
			}}, projections: profileBindings}}, validator: validateProfileBindingCandidate,
		},
		{
			key: "business_references", sourceOwner: "businessreferences", name: "Business references", description: "Dependency graph and direct or transitive impact evidence for installed business definitions.",
			scenarios: scenarios(
				[]string{"An existing product model must be changed safely using dependency and reference-impact evidence"},
				[]string{"The requirement is ordinary CRUD or infrastructure health monitoring"},
				[]string{"reference impact", "dependency graph", "definition consumers"},
				[]string{"business_references.graph", "business_references.impact"}, []string{"identity"}, []string{"audit"},
				[]string{"business_reference_to_safe_model_change"}, nil,
				"Determine every consumer before removing an existing field", "Business References owns direct and transitive dependency evidence",
				"Investigate database latency", "Monitoring owns operational health and metrics"),
			categories: []categorySpec{{key: "business_references.read", name: "Business reference discovery", description: "Inspect direct and transitive dependencies between installed definitions.", chains: []string{"business_reference_to_safe_model_change"}, selectEndpoints: owner("businessreferences")}},
		},
		{
			key: "business_system", sourceOwner: "businesssystem", name: "Business system", description: "Installed Runtime system snapshot plus source-controlled delivery validation and verification.",
			scenarios: scenarios(
				[]string{"A tool must inspect or validate the installed business-system model and its source delivery evidence"},
				[]string{"The requirement is one business record mutation or an infrastructure-only health probe"},
				[]string{"business system snapshot", "delivery verification", "model validation"},
				[]string{"business_system.snapshot", "business_system.validation", "business_system.delivery_verification"}, []string{"identity"}, []string{"audit"},
				[]string{"source_delivery_to_business_system_verification"}, nil,
				"Verify the installed source-controlled model before a governed change", "Business System owns the assembled installed-state evidence",
				"Update one order", "Records owns transactional business data"),
			categories: []categorySpec{{key: "business_system.read", name: "Business-system evidence", description: "Read and validate the installed business-system snapshot and delivery evidence.", chains: []string{"source_delivery_to_business_system_verification"}, selectEndpoints: owner("businesssystem")}},
		},
		{
			key: "runtime_dispatch", sourceOwner: "dispatch", name: "Runtime target execution", description: "Authenticated target-execution boundary for Runtime-resident downstream executors; scheduling remains owned by the Scheduler service.",
			scenarios: scenarios(
				[]string{"An upstream service must invoke an already authorized Runtime-owned Workflow or Report target"},
				[]string{"A caller wants to author schedules, inspect runs, or recover dead letters"},
				[]string{"target execution", "downstream workflow dispatch", "downstream report dispatch"},
				[]string{"dispatch.execution"}, []string{"scheduler"}, []string{"report", "workflow"},
				[]string{"scheduler_run_to_runtime_target_execution"}, nil,
				"Execute a workflow target selected by a Scheduler-owned run", "Runtime Dispatch owns only target acceptance and routing, never scheduling",
				"Create or retry a schedule", "Scheduler owns schedule authoring and execution evidence"),
			categories: []categorySpec{{key: "runtime_dispatch.execution", name: "Runtime target execution", description: "Accept one authenticated request for a Runtime-resident downstream target.", chains: []string{"scheduler_run_to_runtime_target_execution"}, selectEndpoints: owner("dispatch")}},
		},
		{
			key: "notification_bridge", sourceOwner: "notifications", name: "Notification action bridge", description: "Runtime-specific bridge from Notification inbox and delivery evidence to installed business Actions.",
			scenarios: scenarios(
				[]string{"A Runtime client must resolve an installed business Action from an Inbox item or inspect Runtime delivery evidence"},
				[]string{"The requirement authors templates, preferences, policies, providers, or Notification delivery itself"},
				[]string{"inbox business action", "runtime notification delivery evidence"},
				[]string{"notification_bridge.resolve_action", "notification_bridge.delivery_evidence"}, []string{"identity", "notification"}, []string{"records"},
				[]string{"notification_inbox_to_business_action"}, nil,
				"Resolve the approve Action attached to an Inbox notification", "The bridge binds Notification evidence to an installed Runtime Action",
				"Create a notification template", "Notification owns template authoring and delivery"),
			categories: []categorySpec{{key: "notification_bridge.runtime", name: "Notification Runtime bridge", description: "Resolve Inbox actions and inspect Runtime notification delivery evidence.", chains: []string{"notification_inbox_to_business_action"}, selectEndpoints: owner("notifications")}},
		},
		{
			key: "runtime_openapi", sourceOwner: "openapi", name: "Runtime OpenAPI", description: "Machine-readable contract for the exact assembled Runtime HTTP surface.",
			scenarios: scenarios(
				[]string{"A client generator or diagnostic tool needs the assembled Runtime HTTP contract"},
				[]string{"The requirement needs source capability selection or live business data"},
				[]string{"OpenAPI", "HTTP contract", "client generation"},
				[]string{"runtime.openapi"}, nil, nil,
				[]string{"runtime_contract_to_client_generation"}, nil,
				"Generate a client for the assembled Runtime", "Runtime OpenAPI owns the deployed HTTP description",
				"Choose modules for a PRD", "Plane capability aggregation owns module selection"),
			categories: []categorySpec{{key: "runtime_openapi.document", name: "Runtime OpenAPI document", description: "Read the assembled Runtime OpenAPI document.", chains: []string{"runtime_contract_to_client_generation"}, selectEndpoints: owner("openapi")}},
		},
		{
			key: "runtime_operations", sourceOwner: "operations", name: "Runtime operations", description: "Runtime-local operational receipts, controls, diagnostics, recovery, break-glass, and database-retirement evidence.",
			scenarios: scenarios(
				[]string{"An operator must inspect or recover Runtime-local execution and infrastructure state"},
				[]string{"The requirement is a source module's business operation or Scheduler-owned run recovery"},
				[]string{"operation receipt", "dead letter", "break glass", "diagnostics", "database retirement", "lease recovery"},
				[]string{"runtime.operations.receipts", "runtime.operations.controls", "runtime.operations.recovery"}, []string{"identity"}, []string{"audit", "monitoring"},
				[]string{"runtime_failure_to_operator_recovery"}, nil,
				"Recover a stuck Runtime-owned operation from durable evidence", "Runtime Operations owns host-local recovery controls",
				"Retry a Scheduler run", "Scheduler owns its runs and dead letters"),
			categories: []categorySpec{
				{key: "runtime_operations.controls", name: "Runtime operations controls", description: "Mutate Runtime-local controls, recovery, break-glass, lease, idempotency, and database-retirement state.", chains: []string{"runtime_failure_to_operator_recovery"}, selectEndpoints: func(contract endpointmodel.RuntimeEndpointContractV1) bool {
					return endpointOwner(contract) == "operations" && contract.EffectClass == endpointmodel.EndpointEffectWrite
				}},
				{key: "runtime_operations.evidence", name: "Runtime operations evidence", description: "Inspect Runtime-local receipts, controls, diagnostics, runbooks, and recovery evidence.", chains: []string{"runtime_failure_to_operator_recovery"}, selectEndpoints: func(contract endpointmodel.RuntimeEndpointContractV1) bool {
					return endpointOwner(contract) == "operations" && contract.EffectClass == endpointmodel.EndpointEffectRead
				}},
			},
		},
		{
			key: "runtime_core", sourceOwner: "root", name: "Runtime core", description: "Runtime listener identity, liveness, readiness, startup, health, and process metrics.",
			scenarios: scenarios(
				[]string{"Infrastructure must probe or observe one Runtime process"},
				[]string{"The requirement concerns business monitoring definitions or source module operations"},
				[]string{"liveness", "readiness", "startup probe", "runtime health", "process metrics"},
				[]string{"runtime.core.identity", "runtime.core.health", "runtime.core.metrics"}, nil, []string{"monitoring"},
				[]string{"runtime_process_to_operational_probe"}, nil,
				"Check whether a Runtime process is ready to receive traffic", "Runtime Core owns process-level probes",
				"Define an alert policy", "Monitoring owns monitoring policy and evidence"),
			categories: []categorySpec{{key: "runtime_core.operations", name: "Runtime process operations", description: "Read Runtime process identity, probe state, health, and metrics.", chains: []string{"runtime_process_to_operational_probe"}, selectEndpoints: owner("root")}},
		},
		{
			key: "workspace_provision", sourceOwner: "workspaceprovision", name: "Workspace provision", description: "Runtime-local workspace initialization and Identity role reconciliation.",
			scenarios: scenarios(
				[]string{"An operator must initialize a Runtime workspace or reconcile its installed roles"},
				[]string{"The requirement authors a project manifest or manages ordinary Identity users"},
				[]string{"provision workspace", "reconcile workspace roles"},
				[]string{"workspace.provision", "workspace.roles.reconcile"}, []string{"identity"}, []string{"audit"},
				[]string{"workspace_manifest_to_runtime_provision"}, nil,
				"Initialize a workspace and reconcile installed roles", "Workspace Provision owns Runtime-local initialization",
				"Author the project model", "Plane and source control own project authoring"),
			categories: []categorySpec{{key: "workspace_provision.runtime", name: "Runtime workspace provision", description: "Provision a Runtime workspace and reconcile its installed role projection.", chains: []string{"workspace_manifest_to_runtime_provision"}, selectEndpoints: owner("workspaceprovision")}},
		},
	}, nil
}

func modelAPIProjection(operationKeys ...string) ([]modulecapability.SourceProjection, error) {
	payload, err := capabilitycontract.RuntimeModelAPIContractProjection(operationKeys...)
	if err != nil {
		return nil, err
	}
	return []modulecapability.SourceProjection{{Kind: "runtime.api_operations", Key: strings.Join(operationKeys, "+"), Payload: json.RawMessage(payload)}}, nil
}

func buildProvider(document map[string]any, spec providerSpec) (*modulecapability.StaticBinding, error) {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Runtime OpenAPI paths are unavailable")
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Runtime OpenAPI components are unavailable")
	}
	documents := make([]modulecapability.CategoryDocument, 0, len(spec.categories))
	for _, category := range spec.categories {
		operations := map[string]map[string]any{}
		if category.selectEndpoints != nil {
			for _, contract := range endpointmodel.EndpointContracts {
				if !category.selectEndpoints(contract) {
					continue
				}
				method, path, found := strings.Cut(contract.EndpointIdentity, " ")
				if !found {
					return nil, fmt.Errorf("endpoint identity %q is invalid", contract.EndpointIdentity)
				}
				pathItem, _ := paths[path].(map[string]any)
				source, _ := pathItem[strings.ToLower(method)].(map[string]any)
				if source == nil {
					return nil, fmt.Errorf("Runtime OpenAPI operation %s is missing", contract.EndpointIdentity)
				}
				operation, cloneErr := cloneMap(source)
				if cloneErr != nil {
					return nil, cloneErr
				}
				for key := range operation {
					if strings.HasPrefix(key, "x-domainry-") {
						delete(operation, key)
					}
				}
				extension, extensionErr := runtimeOperationExtension(contract)
				if extensionErr != nil {
					return nil, extensionErr
				}
				operation[modulecapability.OperationExtensionKey] = extension
				operations[contract.EndpointIdentity] = operation
			}
		}
		for _, source := range category.actions {
			action, normalizeErr := actioncontract.NormalizeDefinition(source)
			if normalizeErr != nil {
				return nil, fmt.Errorf("category %q Runtime host Action: %w", category.key, normalizeErr)
			}
			if action.HTTP == nil {
				return nil, fmt.Errorf("category %q Runtime host Action %q has no HTTP binding", category.key, action.Key)
			}
			identity := strings.ToUpper(strings.TrimSpace(action.HTTP.Method)) + " " + strings.TrimSpace(action.HTTP.RouteTemplate)
			if _, duplicate := operations[identity]; duplicate {
				return nil, fmt.Errorf("category %q repeats Runtime host operation %s", category.key, identity)
			}
			extension, extensionErr := runtimeActionOperationExtension(spec.sourceOwner, action)
			if extensionErr != nil {
				return nil, extensionErr
			}
			operations[identity] = map[string]any{
				"operationId": action.Key,
				"summary":     action.Label,
				"tags":        []string{category.name},
				"responses": map[string]any{
					"200":     map[string]any{"description": "Successful Runtime host response"},
					"default": map[string]any{"description": "Runtime host error"},
				},
				modulecapability.OperationExtensionKey: extension,
			}
		}
		if len(operations) > modulecapability.MaxCategoryOperations {
			return nil, fmt.Errorf("category %q contains %d operations", category.key, len(operations))
		}
		closure, err := modulecapability.ReferencedComponents(operations, components)
		if err != nil {
			return nil, fmt.Errorf("category %q component closure: %w", category.key, err)
		}
		fragmentPaths := map[string]map[string]json.RawMessage{}
		patterns := make([]string, 0, len(operations))
		for pattern := range operations {
			patterns = append(patterns, pattern)
		}
		sort.Strings(patterns)
		for _, pattern := range patterns {
			method, path, _ := strings.Cut(pattern, " ")
			payload, marshalErr := json.Marshal(operations[pattern])
			if marshalErr != nil {
				return nil, marshalErr
			}
			if fragmentPaths[path] == nil {
				fragmentPaths[path] = map[string]json.RawMessage{}
			}
			fragmentPaths[path][strings.ToLower(method)] = json.RawMessage(payload)
		}
		documents = append(documents, modulecapability.CategoryDocument{
			Category: modulecapability.CategorySummary{Key: category.key, Name: category.name, Description: category.description, OperationCount: len(operations), AssemblyChains: category.chains, ValidationScopes: category.scopes},
			OpenAPI:  modulecapability.OpenAPIFragment{OpenAPI: "3.1.0", Paths: fragmentPaths, Components: closure}, Projections: category.projections,
			ValidationContracts: category.validationContracts,
		})
	}
	summary := modulecapability.ModuleSummary{
		Identity: modulecapability.ModuleIdentity{Key: spec.key, SourceOwner: spec.sourceOwner, ModuleVersion: capabilitycontract.RuntimeCapabilityContractVersion, ValidationRevision: runtimeProviderRevision + "-" + spec.key, SupportedDeploymentModes: []modulecapability.DeploymentMode{modulecapability.DeploymentModeModule}},
		Name:     spec.name, Description: spec.description, Scenarios: spec.scenarios,
	}
	return modulecapability.NewStaticBinding(summary, documents, spec.validator)
}

func endpointOwner(contract endpointmodel.RuntimeEndpointContractV1) string {
	return strings.TrimSpace(contract.SourceOwner)
}

func runtimeOperationExtension(contract endpointmodel.RuntimeEndpointContractV1) (modulecapability.OperationExtension, error) {
	action, err := endpointmodel.AuthorizationActionDefinition(contract)
	if err != nil {
		return modulecapability.OperationExtension{}, err
	}
	authorization := modulecapability.Authorization{
		Strategy: action.Authorization.Strategy, PolicyKey: action.Authorization.PolicyKey,
		Audiences: append([]string(nil), action.Authorization.Audiences...),
	}
	if action.Permission != nil {
		authorization.Permission = action.Permission.Key
	}
	if action.Authorization.Strategy != actioncontract.AuthorizationAnonymous {
		if action.Authorization.Strategy == actioncontract.AuthorizationSigned {
			authorization.WorkspaceScope = "signed_request_workspace"
		} else {
			authorization.WorkspaceScope = "authenticated_workspace"
		}
	}
	value := modulecapability.OperationExtension{
		Owner: endpointOwner(contract), Authorization: authorization, Effect: modulecapability.EffectClass(contract.EffectClass),
		Idempotency: modulecapability.Idempotency{Mode: contract.IdempotencyDecision},
	}
	if contract.EndpointIdentity == http.MethodGet+" /realtime/refresh-events" {
		value.Transport = &modulecapability.Transport{Mode: "sse", ResumeSemantics: "Last-Event-ID is an opaque bounded cursor; a resync event requires authorized state refetch", DeliveryOrdering: "tenant_scoped_refresh_order"}
	}
	return value, nil
}

func runtimeActionOperationExtension(owner string, action actioncontract.ActionDefinition) (modulecapability.OperationExtension, error) {
	authorization := modulecapability.Authorization{
		Strategy:  action.Authorization.Strategy,
		PolicyKey: action.Authorization.PolicyKey,
		Audiences: append([]string(nil), action.Authorization.Audiences...),
	}
	if action.Permission != nil {
		authorization.Permission = action.Permission.Key
	}
	if action.Authorization.Strategy != actioncontract.AuthorizationAnonymous {
		if action.Authorization.Strategy == actioncontract.AuthorizationSigned {
			authorization.WorkspaceScope = "signed_request_workspace"
		} else {
			authorization.WorkspaceScope = "authenticated_workspace"
		}
	}
	return modulecapability.OperationExtension{
		Owner: strings.TrimSpace(owner), Authorization: authorization,
		Effect:      modulecapability.EffectClass(action.EffectClass),
		Idempotency: modulecapability.Idempotency{Mode: strings.TrimSpace(action.IdempotencyDecision)},
	}, nil
}

func cloneMap(value map[string]any) (map[string]any, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func scenarios(useWhen, doNotUseWhen, signals, provided, required, optional, chains, scopes []string, selectRequirement, selectReason, rejectRequirement, rejectReason string) modulecapability.AdaptationScenarios {
	return modulecapability.AdaptationScenarios{
		UseWhen: useWhen, DoNotUseWhen: doNotUseWhen, RequirementSignals: signals, ProvidedCapabilities: provided,
		RequiredModules: required, OptionalModules: optional, ConflictingModules: []string{}, AssemblyChains: chains, ValidationScopes: scopes,
		SelectionExamples: []modulecapability.ScenarioExample{{Requirement: selectRequirement, Reason: selectReason}},
		RejectionExamples: []modulecapability.ScenarioExample{{Requirement: rejectRequirement, Reason: rejectReason}},
	}
}

type authoringProjectionDocument struct {
	Key                string                                        `json:"key"`
	Lifecycle          string                                        `json:"lifecycle"`
	AllowedContexts    []string                                      `json:"allowed_contexts,omitempty"`
	Requires           []string                                      `json:"requires,omitempty"`
	Conflicts          []string                                      `json:"conflicts,omitempty"`
	InputSchema        *capabilitycontract.CapabilityAuthoringSchema `json:"input_schema,omitempty"`
	ReferenceContracts []authoringReference                          `json:"reference_contracts,omitempty"`
}

type authoringReference struct {
	Kind             string `json:"kind"`
	InputJSONPointer string `json:"input_json_pointer"`
	ScopeFrom        string `json:"scope_from,omitempty"`
}

func authoringProjections(definitions []capabilitycontract.CapabilityAuthoringDefinition) ([]modulecapability.SourceProjection, error) {
	result := make([]modulecapability.SourceProjection, 0, len(definitions))
	for _, definition := range definitions {
		references := make([]authoringReference, 0, len(definition.ReferenceContracts))
		for _, reference := range definition.ReferenceContracts {
			references = append(references, authoringReference{Kind: reference.Kind, InputJSONPointer: reference.InputJSONPointer, ScopeFrom: reference.ScopeFrom})
		}
		// A source projection is input to model authoring, not an invocation
		// protocol. InputSchema is the authoritative candidate shape. Parameters
		// duplicate that schema, while examples, errors, outputs and execution
		// describe owner validation or Runtime execution after authoring. Keeping
		// those fields here made every selected category pay for contracts the
		// model neither chooses nor implements.
		value := authoringProjectionDocument{
			Key: definition.Key, Lifecycle: definition.Lifecycle, AllowedContexts: definition.AllowedContexts,
			Requires: definition.Requires, Conflicts: definition.Conflicts, InputSchema: definition.InputSchema, ReferenceContracts: references,
		}
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		result = append(result, modulecapability.SourceProjection{Kind: "runtime.authoring_capability", Key: definition.Key, Payload: json.RawMessage(payload)})
	}
	return result, nil
}

func workflowValidationScopes() []string {
	values := []string{}
	for _, definition := range workflowpolicy.WorkflowAuthoringDomain().Capabilities {
		values = append(values, definition.Key)
	}
	sort.Strings(values)
	return values
}

func automationValidationScopes() []string {
	values := []string{}
	for _, definition := range automationpolicy.AutomationAuthoringDomain().Capabilities {
		values = append(values, definition.Key)
	}
	sort.Strings(values)
	return values
}
